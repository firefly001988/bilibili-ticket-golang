package cluster_service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	deployStatusPending       = "pending"
	deployStatusRunning       = "running"
	deployStatusSucceeded     = "succeeded"
	deployStatusFailed        = "failed"
	deployStatusCancelled     = "cancelled"
	deployStatusPartialFailed = "partial_failed"
)

type RemoteWorkerDeployRequest struct {
	Targets           []RemoteWorkerDeployTarget `json:"targets"`
	PackageType       string                     `json:"packageType,omitempty"` // binary | targz
	BinarySource      string                     `json:"binarySource"`          // local | url
	LocalBinaryPath   string                     `json:"localBinaryPath,omitempty"`
	DownloadURL       string                     `json:"downloadUrl,omitempty"`
	InstallDir        string                     `json:"installDir,omitempty"`
	StartMode         string                     `json:"startMode,omitempty"` // nohup or systemd-user
	OverwriteBinary   bool                       `json:"overwriteBinary"`
	RestartExisting   bool                       `json:"restartExisting"`
	SaveTraffic       bool                       `json:"saveTraffic"`
	Concurrency       int                        `json:"concurrency,omitempty"`
	ConnectionTimeout int                        `json:"connectionTimeoutSec,omitempty"`
	CommandTimeout    int                        `json:"commandTimeoutSec,omitempty"`
}

type RemoteWorkerDeployTarget struct {
	Host       string `json:"host"`
	SSHPort    int    `json:"sshPort,omitempty"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	WorkerPort int    `json:"workerPort,omitempty"`
	Name       string `json:"name,omitempty"`
	WorkerID   string `json:"workerId,omitempty"`
}

type RemoteWorkerDeployJob struct {
	ID         string                         `json:"id"`
	Status     string                         `json:"status"`
	Message    string                         `json:"message,omitempty"`
	StartedAt  time.Time                      `json:"startedAt"`
	FinishedAt *time.Time                     `json:"finishedAt,omitempty"`
	Items      []RemoteWorkerDeployItemStatus `json:"items"`

	cancel context.CancelFunc
}

type RemoteWorkerDeployItemStatus struct {
	Index    int        `json:"index"`
	Host     string     `json:"host"`
	SSHPort  int        `json:"sshPort"`
	WorkerID string     `json:"workerId,omitempty"`
	Name     string     `json:"name,omitempty"`
	Address  string     `json:"address,omitempty"`
	Stage    string     `json:"stage"`
	Status   string     `json:"status"`
	Message  string     `json:"message,omitempty"`
	Logs     []string   `json:"logs,omitempty"`
	Started  time.Time  `json:"startedAt,omitempty"`
	Finished *time.Time `json:"finishedAt,omitempty"`
}

func (s *ClusterService) SelectWorkerBinary() (string, error) {
	if s.wailsApp == nil || s.wailsApp.Dialog == nil {
		return "", fmt.Errorf("file dialog is not available")
	}
	return s.wailsApp.Dialog.OpenFile().
		SetTitle("选择 ticket-worker 文件").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		PromptForSingleSelection()
}

func (s *ClusterService) StartBatchDeployRemoteWorkers(document string) (string, error) {
	var req RemoteWorkerDeployRequest
	if err := json.Unmarshal([]byte(document), &req); err != nil {
		return "", err
	}
	normalizeDeployRequest(&req)
	if err := validateDeployRequest(req); err != nil {
		return "", err
	}
	if req.BinarySource == "local" {
		if stat, err := os.Stat(req.LocalBinaryPath); err != nil {
			return "", fmt.Errorf("local worker package is not readable: %w", err)
		} else if stat.IsDir() {
			return "", fmt.Errorf("local worker package path is a directory")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := &RemoteWorkerDeployJob{
		ID:        randomClusterID("deploy"),
		Status:    deployStatusRunning,
		StartedAt: time.Now(),
		cancel:    cancel,
		Items:     make([]RemoteWorkerDeployItemStatus, len(req.Targets)),
	}
	for i, target := range req.Targets {
		job.Items[i] = RemoteWorkerDeployItemStatus{
			Index:   i,
			Host:    target.Host,
			SSHPort: target.SSHPort,
			Name:    target.Name,
			Stage:   "pending",
			Status:  deployStatusPending,
		}
	}
	s.deployMu.Lock()
	if s.deployJobs == nil {
		s.deployJobs = make(map[string]*RemoteWorkerDeployJob)
	}
	s.deployJobs[job.ID] = job
	s.deployMu.Unlock()

	go s.runRemoteWorkerDeployJob(ctx, job.ID, req)
	return job.ID, nil
}

func (s *ClusterService) GetRemoteWorkerDeployJob(jobID string) (RemoteWorkerDeployJob, error) {
	s.deployMu.RLock()
	defer s.deployMu.RUnlock()
	job, ok := s.deployJobs[jobID]
	if !ok {
		return RemoteWorkerDeployJob{}, fmt.Errorf("deploy job not found")
	}
	return cloneDeployJob(job), nil
}

func (s *ClusterService) CancelRemoteWorkerDeployJob(jobID string) error {
	s.deployMu.RLock()
	job, ok := s.deployJobs[jobID]
	s.deployMu.RUnlock()
	if !ok {
		return fmt.Errorf("deploy job not found")
	}
	if job.cancel != nil {
		job.cancel()
	}
	return nil
}

func normalizeDeployRequest(req *RemoteWorkerDeployRequest) {
	req.PackageType = strings.TrimSpace(strings.ToLower(req.PackageType))
	if req.PackageType == "" {
		req.PackageType = "binary"
	}
	req.BinarySource = strings.TrimSpace(strings.ToLower(req.BinarySource))
	if req.BinarySource == "" {
		req.BinarySource = "local"
	}
	if req.PackageType == "targz" {
		req.SaveTraffic = false
	}
	req.LocalBinaryPath = strings.TrimSpace(req.LocalBinaryPath)
	req.DownloadURL = strings.TrimSpace(req.DownloadURL)
	req.InstallDir = strings.TrimSpace(req.InstallDir)
	if req.InstallDir == "" {
		req.InstallDir = "~/bilibili-ticket-golang"
	}
	req.StartMode = strings.TrimSpace(strings.ToLower(req.StartMode))
	if req.StartMode == "" {
		req.StartMode = "nohup"
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 3
	}
	if req.Concurrency > 10 {
		req.Concurrency = 10
	}
	if req.ConnectionTimeout <= 0 {
		req.ConnectionTimeout = 10
	}
	if req.CommandTimeout <= 0 {
		req.CommandTimeout = 90
	}
	for i := range req.Targets {
		t := &req.Targets[i]
		t.Host = strings.TrimSpace(t.Host)
		t.Username = strings.TrimSpace(t.Username)
		t.Name = strings.TrimSpace(t.Name)
		t.WorkerID = strings.TrimSpace(t.WorkerID)
		if t.SSHPort <= 0 {
			t.SSHPort = 22
		}
		if t.WorkerPort <= 0 {
			t.WorkerPort = 37900
		}
		if t.WorkerID == "" {
			t.WorkerID = generatedWorkerID(t.Host)
		}
		if t.Name == "" {
			t.Name = t.WorkerID
		}
	}
}

func validateDeployRequest(req RemoteWorkerDeployRequest) error {
	if len(req.Targets) == 0 {
		return fmt.Errorf("at least one remote server is required")
	}
	if req.PackageType != "binary" && req.PackageType != "targz" {
		return fmt.Errorf("packageType must be binary or targz")
	}
	if req.BinarySource != "local" && req.BinarySource != "url" {
		return fmt.Errorf("binarySource must be local or url")
	}
	if req.BinarySource == "local" && req.LocalBinaryPath == "" {
		return fmt.Errorf("local worker package path is required")
	}
	if req.BinarySource == "url" && req.DownloadURL == "" {
		return fmt.Errorf("remote download URL is required")
	}
	if req.StartMode != "nohup" && req.StartMode != "systemd-user" {
		return fmt.Errorf("unsupported start mode %q", req.StartMode)
	}
	seenWorkerIDs := make(map[string]struct{}, len(req.Targets))
	for i, target := range req.Targets {
		if target.Host == "" {
			return fmt.Errorf("target %d host is required", i+1)
		}
		if target.Username == "" {
			return fmt.Errorf("target %s SSH username is required", target.Host)
		}
		if target.Password == "" {
			return fmt.Errorf("target %s SSH password is required", target.Host)
		}
		if target.WorkerID == "local" {
			return fmt.Errorf("target %s workerId cannot be local", target.Host)
		}
		if _, exists := seenWorkerIDs[target.WorkerID]; exists {
			return fmt.Errorf("duplicate workerId %q", target.WorkerID)
		}
		seenWorkerIDs[target.WorkerID] = struct{}{}
	}
	return nil
}

func (s *ClusterService) runRemoteWorkerDeployJob(ctx context.Context, jobID string, req RemoteWorkerDeployRequest) {
	sem := make(chan struct{}, req.Concurrency)
	var wg sync.WaitGroup
	for i := range req.Targets {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := s.deployOneRemoteWorker(ctx, jobID, index, req, req.Targets[index]); err != nil {
				status := deployStatusFailed
				if errors.Is(err, context.Canceled) {
					status = deployStatusCancelled
				}
				s.updateDeployItem(jobID, index, "", status, redactDeployError(err, req.Targets[index]), true)
			}
		}(i)
	}
	wg.Wait()
	s.finishDeployJob(jobID, ctx.Err())
}

func (s *ClusterService) deployOneRemoteWorker(ctx context.Context, jobID string, index int, req RemoteWorkerDeployRequest, target RemoteWorkerDeployTarget) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	address := net.JoinHostPort(target.Host, strconv.Itoa(target.WorkerPort))
	s.updateDeployItemMeta(jobID, index, target.WorkerID, target.Name, address)
	s.updateDeployItem(jobID, index, "connecting", deployStatusRunning, "connecting to SSH", false)

	client, err := dialDeploySSH(target, time.Duration(req.ConnectionTimeout)*time.Second)
	if err != nil {
		return fmt.Errorf("connect SSH: %w", err)
	}
	defer client.Close()

	commandTimeout := time.Duration(req.CommandTimeout) * time.Second
	if err = ctx.Err(); err != nil {
		return err
	}
	s.updateDeployItem(jobID, index, "prepare_remote_dir", deployStatusRunning, "creating remote directories", false)
	if err := runDeployScript(ctx, client, remotePrepareScript(req.InstallDir), nil, commandTimeout); err != nil {
		return fmt.Errorf("prepare remote directory: %w", err)
	}

	if req.BinarySource == "local" {
		s.updateDeployItem(jobID, index, "install_binary", deployStatusRunning, "uploading worker package", false)
		if err := s.uploadLocalWorkerPackage(ctx, client, req, commandTimeout); err != nil {
			return err
		}
	} else {
		s.updateDeployItem(jobID, index, "install_binary", deployStatusRunning, "downloading worker package on remote host", false)
		if err := s.downloadRemoteWorkerPackage(ctx, client, req, commandTimeout); err != nil {
			return err
		}
	}

	s.updateDeployItem(jobID, index, "generate_config", deployStatusRunning, "generating worker TLS config", false)
	hosts := strings.Join(uniqueNonEmpty([]string{target.Host, target.WorkerID, "localhost", "127.0.0.1"}), ",")
	generated, err := s.GenerateRemoteWorkerConfig(target.WorkerID, "0.0.0.0:"+strconv.Itoa(target.WorkerPort), hosts)
	if err != nil {
		return fmt.Errorf("generate worker config: %w", err)
	}

	s.updateDeployItem(jobID, index, "import_config", deployStatusRunning, "importing worker config on remote host", false)
	if err := runDeployScript(ctx, client, remoteImportScript(req.InstallDir), strings.NewReader(generated.EncodedConfig), commandTimeout); err != nil {
		return fmt.Errorf("import worker config: %w", err)
	}

	s.updateDeployItem(jobID, index, "start_worker", deployStatusRunning, "starting worker", false)
	if err := runDeployScript(ctx, client, remoteStartScript(req.InstallDir, req.StartMode, req.RestartExisting), nil, commandTimeout); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}

	s.updateDeployItem(jobID, index, "register_employer", deployStatusRunning, "registering worker in employer", false)
	if err := s.AddWorkerFromEncodedConfig(generated.EncodedConfig, address); err != nil {
		return fmt.Errorf("register worker: %w", err)
	}

	s.updateDeployItem(jobID, index, "done", deployStatusSucceeded, "worker deployed and connected", true)
	log.Printf("[cluster] deployed remote worker %s at %s", target.WorkerID, address)
	return nil
}

func (s *ClusterService) uploadLocalWorkerPackage(ctx context.Context, client *ssh.Client, req RemoteWorkerDeployRequest, timeout time.Duration) error {
	if !req.OverwriteBinary {
		if err := runDeployScript(ctx, client, remoteBinaryExistsGuardScript(req.InstallDir), nil, timeout); err != nil {
			return fmt.Errorf("remote worker binary already exists: %w", err)
		}
	}
	file, err := os.Open(req.LocalBinaryPath)
	if err != nil {
		return fmt.Errorf("open local worker package: %w", err)
	}
	defer file.Close()
	switch {
	case req.PackageType == "targz":
		if err := runDeployScript(ctx, client, remoteUploadArchiveScript(req.InstallDir), file, timeout); err != nil {
			return fmt.Errorf("upload worker tar.gz: %w", err)
		}
	case req.SaveTraffic:
		compressed, compressErr := gzipTarSingleFile(ctx, req.LocalBinaryPath, "ticket-worker.new")
		if compressErr != nil {
			return compressErr
		}
		if err := runDeployScript(ctx, client, remoteUploadArchiveScript(req.InstallDir), compressed, timeout); err != nil {
			return fmt.Errorf("upload compressed worker binary: %w", err)
		}
	default:
		if err := runDeployScript(ctx, client, remoteUploadScript(req.InstallDir), file, timeout); err != nil {
			return fmt.Errorf("upload worker binary: %w", err)
		}
	}
	if err := runDeployScript(ctx, client, remoteInstallUploadedScript(req.InstallDir, req.OverwriteBinary), nil, timeout); err != nil {
		return fmt.Errorf("install uploaded worker binary: %w", err)
	}
	return nil
}

func (s *ClusterService) downloadRemoteWorkerPackage(ctx context.Context, client *ssh.Client, req RemoteWorkerDeployRequest, timeout time.Duration) error {
	if req.PackageType == "targz" {
		if err := runDeployScript(ctx, client, remoteDownloadArchiveScript(req.InstallDir, req.DownloadURL, req.OverwriteBinary), nil, timeout); err != nil {
			return fmt.Errorf("download worker tar.gz: %w", err)
		}
		return nil
	}
	if err := runDeployScript(ctx, client, remoteDownloadScript(req.InstallDir, req.DownloadURL, req.OverwriteBinary), nil, timeout); err != nil {
		return fmt.Errorf("download worker binary: %w", err)
	}
	return nil
}

func gzipTarSingleFile(ctx context.Context, sourcePath, entryName string) (io.Reader, error) {
	stat, err := os.Stat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("stat local worker binary: %w", err)
	}
	pr, pw := io.Pipe()
	go func() {
		var pipeErr error
		defer func() {
			_ = pw.CloseWithError(pipeErr)
		}()
		file, err := os.Open(sourcePath)
		if err != nil {
			pipeErr = fmt.Errorf("open local worker binary: %w", err)
			return
		}
		defer file.Close()
		gz := gzip.NewWriter(pw)
		tw := tar.NewWriter(gz)
		header := &tar.Header{
			Name:    entryName,
			Mode:    0600,
			Size:    stat.Size(),
			ModTime: stat.ModTime(),
		}
		if err = tw.WriteHeader(header); err != nil {
			pipeErr = err
			return
		}
		if _, err = io.Copy(tw, file); err != nil {
			pipeErr = err
			return
		}
		if err = tw.Close(); err != nil {
			pipeErr = err
			return
		}
		if err = gz.Close(); err != nil {
			pipeErr = err
			return
		}
		if err = ctx.Err(); err != nil {
			pipeErr = err
		}
	}()
	return pr, nil
}

func dialDeploySSH(target RemoteWorkerDeployTarget, timeout time.Duration) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User:            target.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(target.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}
	return ssh.Dial("tcp", net.JoinHostPort(target.Host, strconv.Itoa(target.SSHPort)), config)
}

func runDeployScript(ctx context.Context, client *ssh.Client, script string, stdin io.Reader, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	var output bytes.Buffer
	session.Stdout = &output
	session.Stderr = &output
	if stdin != nil {
		session.Stdin = stdin
	}
	if err := session.Start("bash -lc " + shellQuote(script)); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return ctx.Err()
	case <-timer.C:
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return fmt.Errorf("remote command timed out after %s: %s", timeout, strings.TrimSpace(output.String()))
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(output.String())
			if msg == "" {
				return err
			}
			return fmt.Errorf("%w: %s", err, msg)
		}
		return nil
	}
}

func remotePrepareScript(installDir string) string {
	return remoteInstallDirAssignment(installDir) + `
set -e
mkdir -p "$INSTALL_DIR/data/worker" "$INSTALL_DIR/logs"
chmod 700 "$INSTALL_DIR" "$INSTALL_DIR/data" "$INSTALL_DIR/data/worker" "$INSTALL_DIR/logs"
`
}

func remoteBinaryExistsGuardScript(installDir string) string {
	return remoteInstallDirAssignment(installDir) + `
set -e
if [ -f "$INSTALL_DIR/ticket-worker" ]; then
  echo "ticket-worker already exists; enable overwrite to replace it"
  exit 17
fi
`
}

func remoteUploadScript(installDir string) string {
	return remoteInstallDirAssignment(installDir) + `
set -e
mkdir -p "$INSTALL_DIR"
cat > "$INSTALL_DIR/ticket-worker.new"
chmod 0600 "$INSTALL_DIR/ticket-worker.new"
`
}

func remoteUploadArchiveScript(installDir string) string {
	return remoteInstallDirAssignment(installDir) + `
set -e
mkdir -p "$INSTALL_DIR"
cat > "$INSTALL_DIR/ticket-worker.tar.gz.new"
unpack_dir="$INSTALL_DIR/.ticket-worker-unpack"
rm -rf "$unpack_dir"
mkdir -p "$unpack_dir"
tar -xzf "$INSTALL_DIR/ticket-worker.tar.gz.new" -C "$unpack_dir"
rm -f "$INSTALL_DIR/ticket-worker.tar.gz.new"
candidate=""
if [ -f "$unpack_dir/ticket-worker" ]; then
  candidate="$unpack_dir/ticket-worker"
elif [ -f "$unpack_dir/ticket-worker.new" ]; then
  candidate="$unpack_dir/ticket-worker.new"
else
  candidate="$(find "$unpack_dir" -type f | head -n 1)"
fi
if [ -z "$candidate" ] || [ ! -f "$candidate" ]; then
  echo "worker tar.gz does not contain a regular file"
  rm -rf "$unpack_dir"
  exit 23
fi
cp "$candidate" "$INSTALL_DIR/ticket-worker.new"
rm -rf "$unpack_dir"
chmod 0600 "$INSTALL_DIR/ticket-worker.new"
`
}

func remoteInstallUploadedScript(installDir string, overwrite bool) string {
	return remoteInstallDirAssignment(installDir) + boolAssignment("OVERWRITE", overwrite) + `
set -e
if [ -f "$INSTALL_DIR/ticket-worker" ] && [ "$OVERWRITE" != "1" ]; then
  rm -f "$INSTALL_DIR/ticket-worker.new"
  echo "ticket-worker already exists; enable overwrite to replace it"
  exit 17
fi
mv "$INSTALL_DIR/ticket-worker.new" "$INSTALL_DIR/ticket-worker"
chmod 0755 "$INSTALL_DIR/ticket-worker"
"$INSTALL_DIR/ticket-worker" version >/dev/null
`
}

func remoteDownloadScript(installDir, url string, overwrite bool) string {
	return remoteInstallDirAssignment(installDir) + boolAssignment("OVERWRITE", overwrite) + "URL=" + shellQuote(url) + `
set -e
if [ -f "$INSTALL_DIR/ticket-worker" ] && [ "$OVERWRITE" != "1" ]; then
  echo "ticket-worker already exists; enable overwrite to replace it"
  exit 17
fi
if command -v curl >/dev/null 2>&1; then
  curl -fL --retry 3 -o "$INSTALL_DIR/ticket-worker.new" "$URL"
elif command -v wget >/dev/null 2>&1; then
  wget -O "$INSTALL_DIR/ticket-worker.new" "$URL"
else
  echo "curl or wget is required for remote download"
  exit 18
fi
mv "$INSTALL_DIR/ticket-worker.new" "$INSTALL_DIR/ticket-worker"
chmod 0755 "$INSTALL_DIR/ticket-worker"
"$INSTALL_DIR/ticket-worker" version >/dev/null
`
}

func remoteDownloadArchiveScript(installDir, url string, overwrite bool) string {
	return remoteInstallDirAssignment(installDir) + boolAssignment("OVERWRITE", overwrite) + "URL=" + shellQuote(url) + `
set -e
if [ -f "$INSTALL_DIR/ticket-worker" ] && [ "$OVERWRITE" != "1" ]; then
  echo "ticket-worker already exists; enable overwrite to replace it"
  exit 17
fi
mkdir -p "$INSTALL_DIR"
if command -v curl >/dev/null 2>&1; then
  curl -fL --retry 3 -o "$INSTALL_DIR/ticket-worker.tar.gz.new" "$URL"
elif command -v wget >/dev/null 2>&1; then
  wget -O "$INSTALL_DIR/ticket-worker.tar.gz.new" "$URL"
else
  echo "curl or wget is required for remote download"
  exit 18
fi
unpack_dir="$INSTALL_DIR/.ticket-worker-unpack"
rm -rf "$unpack_dir"
mkdir -p "$unpack_dir"
tar -xzf "$INSTALL_DIR/ticket-worker.tar.gz.new" -C "$unpack_dir"
rm -f "$INSTALL_DIR/ticket-worker.tar.gz.new"
candidate=""
if [ -f "$unpack_dir/ticket-worker" ]; then
  candidate="$unpack_dir/ticket-worker"
elif [ -f "$unpack_dir/ticket-worker.new" ]; then
  candidate="$unpack_dir/ticket-worker.new"
else
  candidate="$(find "$unpack_dir" -type f | head -n 1)"
fi
if [ -z "$candidate" ] || [ ! -f "$candidate" ]; then
  echo "worker tar.gz does not contain a regular file"
  rm -rf "$unpack_dir"
  exit 23
fi
cp "$candidate" "$INSTALL_DIR/ticket-worker.new"
rm -rf "$unpack_dir"
if [ -f "$INSTALL_DIR/ticket-worker" ] && [ "$OVERWRITE" != "1" ]; then
  rm -f "$INSTALL_DIR/ticket-worker.new"
  echo "ticket-worker already exists; enable overwrite to replace it"
  exit 17
fi
mv "$INSTALL_DIR/ticket-worker.new" "$INSTALL_DIR/ticket-worker"
chmod 0755 "$INSTALL_DIR/ticket-worker"
"$INSTALL_DIR/ticket-worker" version >/dev/null
`
}

func remoteImportScript(installDir string) string {
	return remoteInstallDirAssignment(installDir) + `
set -e
"$INSTALL_DIR/ticket-worker" import --stdin --o "$INSTALL_DIR/data/worker"
`
}

func remoteStartScript(installDir, mode string, restart bool) string {
	if mode == "systemd-user" {
		return remoteStartSystemdUserScript(installDir, restart)
	}
	return remoteStartNohupScript(installDir, restart)
}

func remoteStartNohupScript(installDir string, restart bool) string {
	return remoteInstallDirAssignment(installDir) + boolAssignment("RESTART", restart) + `
set -e
if [ -f "$INSTALL_DIR/worker.pid" ]; then
  old_pid="$(cat "$INSTALL_DIR/worker.pid" 2>/dev/null || true)"
  if [ -n "$old_pid" ] && kill -0 "$old_pid" >/dev/null 2>&1; then
    if [ "$RESTART" = "1" ]; then
      kill "$old_pid" >/dev/null 2>&1 || true
      sleep 1
    else
      echo "worker is already running; enable restart to replace it"
      exit 19
    fi
  fi
fi
cd "$INSTALL_DIR"
nohup ./ticket-worker serve --config "$INSTALL_DIR/data/worker/worker.json" > "$INSTALL_DIR/logs/worker.log" 2>&1 < /dev/null &
echo $! > "$INSTALL_DIR/worker.pid"
sleep 1
new_pid="$(cat "$INSTALL_DIR/worker.pid")"
if ! kill -0 "$new_pid" >/dev/null 2>&1; then
  echo "worker failed to stay running"
  tail -80 "$INSTALL_DIR/logs/worker.log" 2>/dev/null || true
  exit 20
fi
`
}

func remoteStartSystemdUserScript(installDir string, restart bool) string {
	return remoteInstallDirAssignment(installDir) + boolAssignment("RESTART", restart) + `
set -e
if ! command -v systemctl >/dev/null 2>&1; then
  echo "systemctl is required for systemd --user start mode"
  exit 21
fi
if ! systemctl --user show-environment >/dev/null 2>&1; then
  echo "systemd user manager is unavailable; enable user lingering or use nohup start mode"
  exit 22
fi
mkdir -p "$HOME/.config/systemd/user" "$INSTALL_DIR/logs"
SERVICE_FILE="$HOME/.config/systemd/user/bilibili-ticket-worker.service"
cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Bilibili Ticket Worker
After=network-online.target

[Service]
Type=simple
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/ticket-worker serve --config $INSTALL_DIR/data/worker/worker.json
Restart=always
RestartSec=3
StandardOutput=append:$INSTALL_DIR/logs/worker.log
StandardError=append:$INSTALL_DIR/logs/worker.log

[Install]
WantedBy=default.target
EOF
systemctl --user daemon-reload
systemctl --user enable bilibili-ticket-worker.service >/dev/null
if systemctl --user is-active --quiet bilibili-ticket-worker.service; then
  if [ "$RESTART" = "1" ]; then
    systemctl --user restart bilibili-ticket-worker.service
  else
    echo "worker service is already running; enable restart to replace it"
    exit 19
  fi
else
  systemctl --user start bilibili-ticket-worker.service
fi
sleep 1
if ! systemctl --user is-active --quiet bilibili-ticket-worker.service; then
  echo "worker service failed to stay running"
  systemctl --user status bilibili-ticket-worker.service --no-pager 2>/dev/null || true
  tail -80 "$INSTALL_DIR/logs/worker.log" 2>/dev/null || true
  exit 20
fi
`
}

func remoteInstallDirAssignment(installDir string) string {
	if installDir == "~" {
		return "INSTALL_DIR=$HOME\n"
	}
	if strings.HasPrefix(installDir, "~/") {
		return "INSTALL_DIR=$HOME/" + shellQuote(strings.TrimPrefix(installDir, "~/")) + "\n"
	}
	return "INSTALL_DIR=" + shellQuote(installDir) + "\n"
}

func boolAssignment(name string, value bool) string {
	if value {
		return name + "=1\n"
	}
	return name + "=0\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func generatedWorkerID(host string) string {
	base := strings.ToLower(host)
	base = strings.Trim(base, "[]")
	re := regexp.MustCompile(`[^a-z0-9]+`)
	base = strings.Trim(re.ReplaceAllString(base, "-"), "-")
	if base == "" {
		base = "remote"
	}
	if len(base) > 32 {
		base = base[:32]
		base = strings.Trim(base, "-")
	}
	return "worker-" + base
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func redactDeployError(err error, target RemoteWorkerDeployTarget) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if target.Password != "" {
		msg = strings.ReplaceAll(msg, target.Password, "******")
	}
	return msg
}

func (s *ClusterService) updateDeployItemMeta(jobID string, index int, workerID, name, address string) {
	s.deployMu.Lock()
	defer s.deployMu.Unlock()
	job := s.deployJobs[jobID]
	if job == nil || index < 0 || index >= len(job.Items) {
		return
	}
	job.Items[index].WorkerID = workerID
	job.Items[index].Name = name
	job.Items[index].Address = address
}

func (s *ClusterService) updateDeployItem(jobID string, index int, stage, status, message string, finished bool) {
	s.deployMu.Lock()
	defer s.deployMu.Unlock()
	job := s.deployJobs[jobID]
	if job == nil || index < 0 || index >= len(job.Items) {
		return
	}
	item := &job.Items[index]
	if !item.Started.IsZero() || status == deployStatusRunning {
		if item.Started.IsZero() {
			item.Started = time.Now()
		}
	}
	if stage != "" {
		item.Stage = stage
	}
	if status != "" {
		item.Status = status
	}
	if message != "" {
		item.Message = message
		item.Logs = append(item.Logs, fmt.Sprintf("%s %s: %s", time.Now().Format("15:04:05"), item.Stage, message))
		if len(item.Logs) > 200 {
			item.Logs = append([]string(nil), item.Logs[len(item.Logs)-200:]...)
		}
	}
	if finished {
		now := time.Now()
		item.Finished = &now
	}
}

func (s *ClusterService) finishDeployJob(jobID string, ctxErr error) {
	s.deployMu.Lock()
	defer s.deployMu.Unlock()
	job := s.deployJobs[jobID]
	if job == nil {
		return
	}
	succeeded, failed, cancelled := 0, 0, 0
	for i := range job.Items {
		switch job.Items[i].Status {
		case deployStatusSucceeded:
			succeeded++
		case deployStatusCancelled:
			cancelled++
		case deployStatusFailed:
			failed++
		default:
			if errors.Is(ctxErr, context.Canceled) {
				cancelled++
				job.Items[i].Status = deployStatusCancelled
				job.Items[i].Message = "deployment cancelled"
			} else {
				failed++
				job.Items[i].Status = deployStatusFailed
				job.Items[i].Message = "deployment did not finish"
			}
		}
	}
	switch {
	case succeeded == len(job.Items):
		job.Status = deployStatusSucceeded
		job.Message = fmt.Sprintf("%d worker(s) deployed", succeeded)
	case cancelled == len(job.Items):
		job.Status = deployStatusCancelled
		job.Message = "deployment cancelled"
	case succeeded > 0:
		job.Status = deployStatusPartialFailed
		job.Message = fmt.Sprintf("%d succeeded, %d failed, %d cancelled", succeeded, failed, cancelled)
	default:
		job.Status = deployStatusFailed
		job.Message = fmt.Sprintf("%d failed, %d cancelled", failed, cancelled)
	}
	now := time.Now()
	job.FinishedAt = &now
}

func cloneDeployJob(job *RemoteWorkerDeployJob) RemoteWorkerDeployJob {
	clone := *job
	clone.cancel = nil
	clone.Items = append([]RemoteWorkerDeployItemStatus(nil), job.Items...)
	for i := range clone.Items {
		clone.Items[i].Logs = append([]string(nil), job.Items[i].Logs...)
	}
	sort.SliceStable(clone.Items, func(i, j int) bool { return clone.Items[i].Index < clone.Items[j].Index })
	return clone
}
