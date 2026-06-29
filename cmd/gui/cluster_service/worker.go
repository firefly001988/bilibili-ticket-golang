package cluster_service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/employer"
	clusterworker "bilibili-ticket-golang/cluster/worker"
	"bilibili-ticket-golang/lib/global"
)

// AddWorker registers a new remote worker and verifies connectivity.
func (s *ClusterService) AddWorker(document string) error {
	var input struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Address       string `json:"address"`
		CACert        string `json:"caCert"`
		ClientCert    string `json:"clientCert"`
		ClientKey     string `json:"clientKey"`
		TLSServerName string `json:"tlsServerName"`
		Force         bool   `json:"force"`
	}
	if err := json.Unmarshal([]byte(document), &input); err != nil {
		return err
	}
	if input.ID == "local" {
		return fmt.Errorf("the local worker is automatically managed and cannot be added manually")
	}
	if input.ID == "" || input.Address == "" || input.ClientKey == "" {
		return fmt.Errorf("id, address and clientKey are required")
	}
	node := domain.WorkerNode{
		ID:            input.ID,
		Name:          input.Name,
		Address:       input.Address,
		Type:          domain.WorkerTypeRemote,
		Enabled:       true,
		TLSServerName: input.TLSServerName,
	}
	tlsConfig := domain.WorkerTLSConfig{
		CACertPEM:     []byte(input.CACert),
		ClientCertPEM: []byte(input.ClientCert),
		ClientKeyPEM:  []byte(input.ClientKey),
		ServerName:    input.TLSServerName,
	}
	if err := s.client.SetTLSFromConfig(node.ID, tlsConfig); err != nil {
		return fmt.Errorf("invalid TLS config: %w", err)
	}
	ctx := context.Background()

	// Synchronously dial the new worker so connection errors surface
	// immediately instead of waiting for the async health check.
	healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var healthErr error
	if input.Force {
		_, healthErr = s.client.HealthForce(healthCtx, node)
	} else {
		_, healthErr = s.client.Health(healthCtx, node)
	}
	if healthErr != nil {
		s.client.RemoveTLS(node.ID)
		log.Printf("[cluster] health check for new worker %s (%s): %v", node.ID, node.Address, healthErr)
		return fmt.Errorf("worker unreachable: %w", healthErr)
	}

	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	if err := s.repository.PutWorkerTLS(ctx, node.ID, tlsConfig); err != nil {
		return err
	}
	if err := s.refreshResources(ctx); err != nil {
		return err
	}

	log.Printf("[cluster] worker %s connected (%s)", node.ID, node.Address)
	return nil
}

// UpdateWorker updates the connection settings (address, TLS, role) for an
// existing worker. The worker must not have active attempts. Accepts the
// same JSON document shape as AddWorker.
func (s *ClusterService) UpdateWorker(document string) error {
	var input struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Address       string `json:"address"`
		CACert        string `json:"caCert"`
		ClientCert    string `json:"clientCert"`
		ClientKey     string `json:"clientKey"`
		TLSServerName string `json:"tlsServerName"`
	}
	if err := json.Unmarshal([]byte(document), &input); err != nil {
		return err
	}
	if input.ID == "" || input.Address == "" {
		return fmt.Errorf("id and address are required")
	}
	if input.ID == "local" {
		return fmt.Errorf("the automatically managed local worker cannot be edited")
	}
	// Block if the worker is executing an active attempt.
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.WorkerID == input.ID && !attempt.State.Terminal() {
			return fmt.Errorf("worker is used by active attempt %s", attempt.ID)
		}
	}
	node := domain.WorkerNode{
		ID:            input.ID,
		Name:          input.Name,
		Address:       input.Address,
		Type:          domain.WorkerTypeRemote,
		Enabled:       true,
		TLSServerName: input.TLSServerName,
	}
	ctx := context.Background()
	tlsConfig := domain.WorkerTLSConfig{
		CACertPEM:     []byte(input.CACert),
		ClientCertPEM: []byte(input.ClientCert),
		ClientKeyPEM:  []byte(input.ClientKey),
		ServerName:    input.TLSServerName,
	}
	// If no TLS credentials were provided, retain the existing TLS config.
	if input.ClientKey == "" {
		existingTLS, err := s.repository.WorkerTLS(ctx, input.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("fetch existing TLS config: %w", err)
		}
		if err == nil && len(existingTLS.ClientKeyPEM) > 0 {
			tlsConfig = existingTLS
		}
	}
	// Close existing connection before applying new TLS config.
	s.client.RemoveTLS(input.ID)
	if err := s.client.SetTLSFromConfig(node.ID, tlsConfig); err != nil {
		return fmt.Errorf("invalid TLS config: %w", err)
	}

	// Synchronously dial the updated worker so connection errors surface
	// immediately instead of waiting for the async health check.
	healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.client.Health(healthCtx, node); err != nil {
		s.client.RemoveTLS(node.ID)
		log.Printf("[cluster] health check for updated worker %s (%s): %v", node.ID, node.Address, err)
		return fmt.Errorf("worker unreachable: %w", err)
	}

	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	if err := s.repository.PutWorkerTLS(ctx, node.ID, tlsConfig); err != nil {
		return err
	}
	if err := s.refreshResources(ctx); err != nil {
		return err
	}

	log.Printf("[cluster] worker %s reconnected (%s)", node.ID, node.Address)
	return nil
}

// AddLocalWorker creates and starts a new in-process local worker with
// the given ID, name and listen address. If id is empty, one is generated.
// The primary "local" worker is automatically managed — callers must not
// attempt to create it manually.
func (s *ClusterService) AddLocalWorker(id, name, listen string) error {
	if id == "local" {
		return fmt.Errorf("the local worker is automatically managed and cannot be added manually")
	}
	ctx := context.Background()
	pluginName := ""
	if _, statErr := os.Stat("plugins/captcha-plugin"); statErr == nil {
		pluginName = "captcha-plugin"
	}
	node, err := s.local.AddWorker(ctx, s.client, id, name, listen, employer.LocalWorkerOptions{
		PluginDir:     "plugins",
		CaptchaPlugin: pluginName,
		Version:       global.GitCommit,
	})
	if err != nil {
		return err
	}
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	tlsBundle, _, tlsErr := clusterworker.LoadOrGenerateLocalTLS("data/" + node.ID)
	if tlsErr == nil {
		_ = s.repository.PutWorkerTLS(ctx, node.ID, domain.WorkerTLSConfig{
			CACertPEM:     tlsBundle.CAPEM,
			ClientCertPEM: tlsBundle.CertPEM,
			ClientKeyPEM:  tlsBundle.KeyPEM,
			ServerName:    "localhost",
		})
	}
	return s.refreshResources(ctx)
}

// StartLocalWorker starts (or restarts) an existing local worker by ID.
// If the worker was stopped with an older version that removed the slot,
// this falls back to AddWorker using the node data stored in the repository.
func (s *ClusterService) StartLocalWorker(workerID string) error {
	ctx := context.Background()
	pluginName := ""
	if _, statErr := os.Stat("plugins/captcha-plugin"); statErr == nil {
		pluginName = "captcha-plugin"
	}
	opts := employer.LocalWorkerOptions{
		PluginDir:     "plugins",
		CaptchaPlugin: pluginName,
		Version:       global.GitCommit,
	}
	node, err := s.local.StartWorker(ctx, s.client, workerID, opts)
	if err != nil && strings.Contains(err.Error(), "not added yet") {
		// The slot was destroyed by an older StopWorker version, or the
		// app was restarted after a stop.  Recover from the repository.
		repoNode, repoErr := s.repository.Worker(ctx, workerID)
		if repoErr != nil {
			return fmt.Errorf("worker %q not found in repository: %w", workerID, repoErr)
		}
		node, err = s.local.AddWorker(ctx, s.client, workerID, repoNode.Name, repoNode.Address, opts)
	}
	if err != nil {
		return err
	}
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	tlsBundle, _, tlsErr := clusterworker.LoadOrGenerateLocalTLS("data/" + workerID)
	if tlsErr == nil {
		_ = s.repository.PutWorkerTLS(ctx, node.ID, domain.WorkerTLSConfig{
			CACertPEM:     tlsBundle.CAPEM,
			ClientCertPEM: tlsBundle.CertPEM,
			ClientKeyPEM:  tlsBundle.KeyPEM,
			ServerName:    "localhost",
		})
	}
	return s.refreshResources(ctx)
}

// StopLocalWorker stops a local in-process worker without removing it from
// the repository. The worker stays enabled so the frontend shows it as
// "offline" with a start button. The gRPC client connection is closed
// immediately so IsHealthy returns false right away.
// The primary "local" worker can never be stopped.
func (s *ClusterService) StopLocalWorker(workerID string) error {
	if workerID == "local" {
		return fmt.Errorf("the local worker cannot be stopped")
	}
	if err := s.local.StopWorker(workerID); err != nil {
		return err
	}
	// Tear down the gRPC client connection immediately so the frontend sees
	// the worker as offline without waiting for the heartbeat timeout (15 s).
	s.client.CloseConnection(workerID)
	return s.refreshResources(context.Background())
}

// WorkerConfigResponse returns the full configuration for a worker, suitable
// for pre-filling an edit form.
type WorkerConfigResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	CACert        string `json:"caCert"`
	ClientCert    string `json:"clientCert"`
	ClientKey     string `json:"clientKey"`
	TLSServerName string `json:"tlsServerName"`
}

// GetWorkerConfig reads the full connection settings for a worker (node info
// plus TLS PEM material) so that the frontend can pre-fill the edit form.
func (s *ClusterService) GetWorkerConfig(workerID string) (WorkerConfigResponse, error) {
	ctx := context.Background()
	node, err := s.repository.Worker(ctx, workerID)
	if err != nil {
		return WorkerConfigResponse{}, fmt.Errorf("worker %s not found: %w", workerID, err)
	}
	tlsConfig, err := s.repository.WorkerTLS(ctx, workerID)
	if err != nil {
		return WorkerConfigResponse{}, fmt.Errorf("TLS config for worker %s not found: %w", workerID, err)
	}
	return WorkerConfigResponse{
		ID:            node.ID,
		Name:          node.Name,
		Address:       node.Address,
		CACert:        string(tlsConfig.CACertPEM),
		ClientCert:    string(tlsConfig.ClientCertPEM),
		ClientKey:     string(tlsConfig.ClientKeyPEM),
		TLSServerName: node.TLSServerName,
	}, nil
}

// DeleteWorker removes a worker. Local workers are stopped first.
func (s *ClusterService) DeleteWorker(workerID string) error {
	if workerID == "local" {
		return fmt.Errorf("the automatically managed local worker cannot be deleted")
	}
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.WorkerID == workerID && !attempt.State.Terminal() {
			return fmt.Errorf("worker is used by active attempt %s", attempt.ID)
		}
	}
	// If this is a local in-process worker, stop it first.
	_ = s.local.RemoveWorker(workerID)
	if err := s.repository.DeleteWorker(context.Background(), workerID); err != nil {
		return err
	}
	s.client.RemoveTLS(workerID)
	return s.refreshResources(context.Background())
}

// DisconnectWorker closes the gRPC connection to a worker (keeping the TLS
// config so it can be reconnected later).
func (s *ClusterService) DisconnectWorker(workerID string) error {
	if workerID == "local" {
		return fmt.Errorf("the local worker cannot be disconnected")
	}
	s.client.Disconnect(workerID)
	return s.refreshResources(context.Background())
}

// ReconnectWorker re-establishes the gRPC connection to a worker and verifies
// it with a health check.  Retries up to 5 times with 5s intervals.
func (s *ClusterService) ReconnectWorker(workerID string) error {
	if workerID == "local" {
		return fmt.Errorf("the local worker is auto-managed")
	}
	ctx := context.Background()
	node, err := s.repository.Worker(ctx, workerID)
	if err != nil {
		return fmt.Errorf("worker %s not found: %w", workerID, err)
	}
	// Close any stale connection first.
	s.client.Disconnect(workerID)

	const maxRetries = 5
	const retryInterval = 5 * time.Second
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		healthCtx, cancel := context.WithTimeout(ctx, retryInterval)
		_, err := s.client.Health(healthCtx, node)
		cancel()
		if err == nil {
			log.Printf("[cluster] worker %s reconnected (attempt %d)", workerID, i+1)
			return s.refreshResources(ctx)
		}
		lastErr = err
		log.Printf("[cluster] reconnect worker %s attempt %d/%d failed: %v", workerID, i+1, maxRetries, err)
		if i < maxRetries-1 {
			time.Sleep(retryInterval)
		}
	}
	return fmt.Errorf("reconnect failed after %d attempts: %w", maxRetries, lastErr)
}

// ForceReconnectWorker bypasses the protocol version check, reconnects
// the worker, and persists SkipVersionCheck=true so future Health calls
// also skip the check.
func (s *ClusterService) ForceReconnectWorker(workerID string) error {
	if workerID == "local" {
		return fmt.Errorf("the local worker is auto-managed")
	}
	ctx := context.Background()
	node, err := s.repository.Worker(ctx, workerID)
	if err != nil {
		return fmt.Errorf("worker %s not found: %w", workerID, err)
	}
	s.client.Disconnect(workerID)

	node.SkipVersionCheck = true
	node.Version = node.Version // keep existing
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}

	healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := s.client.HealthForce(healthCtx, node)
	if err != nil {
		return fmt.Errorf("force reconnect failed: %w", err)
	}
	// Update the worker's version to the real one reported by the worker.
	if v, ok := info["version"].(string); ok && v != "" {
		node.Version = v
		_ = s.repository.PutWorker(ctx, node)
	}
	return s.refreshResources(ctx)
}

// GenerateRemoteWorkerConfigResponse is returned by GenerateRemoteWorkerConfig.
type GenerateRemoteWorkerConfigResponse struct {
	EncodedConfig string `json:"encodedConfig"` // Base4096 string for the worker
	WorkerID      string `json:"workerId"`
	Listen        string `json:"listen"`
}

// GenerateRemoteWorkerConfig creates TLS material and a complete configuration
// for a remote worker, then encodes it as a Base4096 string for copy-paste
// distribution.  Only the employer-side TLS credentials are stored so that
// mTLS works once the worker comes online; the worker is **not** added to
// the repository list (the user must add it manually via AddWorker after
// the worker is deployed).
//
// Parameters:
//   - workerID: unique identifier for the worker (e.g. "home-server")
//   - listen: address the worker will listen on (e.g. "0.0.0.0:37900")
//   - hosts: comma-separated list of DNS names / IPs for the server TLS cert
//     (e.g. "myworker.example.com,192.168.1.100")
func (s *ClusterService) GenerateRemoteWorkerConfig(workerID, listen, hosts string) (GenerateRemoteWorkerConfigResponse, error) {
	if workerID == "" {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("workerId is required")
	}
	if workerID == "local" {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("'local' is a reserved name; please choose a different worker ID")
	}
	if listen == "" {
		listen = "0.0.0.0:37900"
	}
	hostList := strings.Split(hosts, ",")
	filtered := hostList[:0]
	for _, h := range hostList {
		h = strings.TrimSpace(h)
		if h != "" {
			filtered = append(filtered, h)
		}
	}
	hostList = filtered
	if len(hostList) == 0 {
		hostList = []string{"localhost", "127.0.0.1"}
	}

	// Load the employer's persistent CA and client certificate from disk.
	// These are auto-generated by LoadOrGenerateLocalTLS on startup and
	// stored in data/local-worker/.  Reusing them gives the employer a
	// single identity across all remote workers.
	employerTLSBundle, _, tlsLoadErr := clusterworker.LoadOrGenerateLocalTLS("data/local-worker")
	if tlsLoadErr != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("load employer TLS: %w", tlsLoadErr)
	}
	caCertPEM := employerTLSBundle.CAPEM
	clientCertPEM := employerTLSBundle.CertPEM
	clientKeyPEM := employerTLSBundle.KeyPEM

	// Load the CA private key (needed for signing server certs).
	caKeyPEM, caKeyErr := os.ReadFile("data/local-worker/ca-key.pem")
	if caKeyErr != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("read employer CA key: %w", caKeyErr)
	}

	rc, bundle, err := clusterworker.GenerateRemoteWorkerConfig(
		caCertPEM, caKeyPEM, clientCertPEM, clientKeyPEM,
		hostList, workerID,
		clusterworker.RemoteWorkerOptions{
			Listen:          listen,
			WorkerID:        workerID,
			DataDir:         "data/worker",
			PollIntervalSec: 15,
			CalibrateClock:  true,
			Version:         global.GitCommit,
		},
	)
	if err != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("generate remote worker config: %w", err)
	}

	encoded, err := rc.Encode()
	if err != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("encode config: %w", err)
	}

	// Register the employer-side TLS credentials both in-memory and in the
	// repository so the worker can be added later via AddWorkerFromEncodedConfig.
	tlsConfig := domain.WorkerTLSConfig{
		CACertPEM:     bundle.CAPEM,
		ClientCertPEM: bundle.CertPEM,
		ClientKeyPEM:  bundle.KeyPEM,
		ServerName:    bundle.ServerName,
	}
	// Keep TLS in memory only — the worker row does not exist yet in the
	// workers table, so PutWorkerTLS would fail with an FK constraint.
	// Persistence happens later when the worker is actually added via
	// AddWorker or AddWorkerFromEncodedConfig.
	if err := s.client.SetTLSFromConfig(workerID, tlsConfig); err != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("set worker TLS: %w", err)
	}

	return GenerateRemoteWorkerConfigResponse{
		EncodedConfig: encoded,
		WorkerID:      workerID,
		Listen:        listen,
	}, nil
}

// AddWorkerFromEncodedConfig decodes a Base4096-encoded worker configuration
// and adds the worker to the repository.  The encoded string carries both
// the worker-side and employer-side TLS material, so no prior TLS setup
// is required — it is extracted directly from the config.
//
// overrideAddress, when non-empty, overrides the dial address (rc.Listen)
// embedded in the encoded config.  This allows the employer to specify the
// real reachable IP:port of the worker, which may differ from the listen
// address the worker was configured with (e.g. 0.0.0.0:37900 vs the actual
// public IP).
func (s *ClusterService) AddWorkerFromEncodedConfig(encodedConfig string, overrideAddress string) error {
	rc, err := clusterworker.DecodeRemoteWorkerConfig(encodedConfig)
	if err != nil {
		return fmt.Errorf("decode worker config: %w", err)
	}
	if rc.WorkerID == "" || rc.Listen == "" {
		return fmt.Errorf("worker config missing required fields (workerId, listen)")
	}
	if rc.WorkerID == "local" {
		return fmt.Errorf("cannot import the local worker")
	}
	ctx := context.Background()

	// Determine the dial address.  Prefer the override supplied by the
	// employer (the real IP:port of the employee's machine); fall back to
	// the listen address embedded in the config.
	// If the override address has no port, keep the port from rc.Listen.
	address := rc.Listen
	if overrideAddress != "" {
		if !strings.Contains(overrideAddress, ":") {
			// No port given — extract port from rc.Listen and append it.
			if _, port, portErr := net.SplitHostPort(rc.Listen); portErr == nil {
				overrideAddress = net.JoinHostPort(overrideAddress, port)
			}
		}
		address = overrideAddress
	}

	// Build TLS config.  Prefer the employer fields embedded in the encoded
	// string (populated by GenerateRemoteWorkerConfig).  Fall back to the
	// repository for configs generated before employer fields were added.
	var tlsConfig domain.WorkerTLSConfig
	if rc.EmployerCertPEM != "" && rc.EmployerKeyPEM != "" {
		tlsConfig = domain.WorkerTLSConfig{
			CACertPEM:     []byte(rc.CACertPEM),
			ClientCertPEM: []byte(rc.EmployerCertPEM),
			ClientKeyPEM:  []byte(rc.EmployerKeyPEM),
		}
	} else {
		stored, tlsErr := s.repository.WorkerTLS(ctx, rc.WorkerID)
		if tlsErr != nil {
			return fmt.Errorf("no TLS credentials found for worker %q — the encoded config is too old (missing employer fields); run 'Generate Remote Worker Config' first to create a new config, or add the worker manually with CA/client cert/key", rc.WorkerID)
		}
		tlsConfig = stored
	}

	// Set TLS server name from the worker node or derive from config.
	if tlsConfig.ServerName == "" {
		// Use the worker ID as a fallback SNI hostname.
		tlsConfig.ServerName = rc.WorkerID
	}

	if err := s.client.SetTLSFromConfig(rc.WorkerID, tlsConfig); err != nil {
		return fmt.Errorf("apply TLS config: %w", err)
	}

	node := domain.WorkerNode{
		ID:            rc.WorkerID,
		Address:       address,
		Type:          domain.WorkerTypeRemote,
		Version:       rc.Version,
		Enabled:       true,
		TLSServerName: tlsConfig.ServerName,
	}
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	if err := s.repository.PutWorkerTLS(ctx, rc.WorkerID, tlsConfig); err != nil {
		return err
	}
	if err := s.refreshResources(ctx); err != nil {
		return err
	}

	// Synchronously dial the imported worker so connection errors surface
	// immediately instead of waiting for the async health check.
	// Protocol version mismatches are returned to the caller so the
	// frontend can offer a "force connect" option.
	healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.client.Health(healthCtx, node); err != nil {
		if strings.Contains(err.Error(), "version mismatch") || strings.Contains(err.Error(), "protocol version mismatch") {
			// Version mismatch — rollback: remove the worker row and TLS so
			// the caller can decide to retry with ForceAdd.
			_ = s.repository.DeleteWorker(ctx, node.ID)
			s.client.RemoveTLS(node.ID)
			return err
		}
		log.Printf("[cluster] health check for imported worker %s (%s): %v", node.ID, node.Address, err)
		// Other errors — the worker row is saved and will be retried later.
	} else {
		log.Printf("[cluster] worker %s connected (%s)", node.ID, node.Address)
	}
	return nil
}

// ForceAddWorkerFromEncodedConfig is identical to AddWorkerFromEncodedConfig
// except it bypasses the protocol version check by calling HealthForce.
// Use only when the user has explicitly acknowledged the version mismatch.
func (s *ClusterService) ForceAddWorkerFromEncodedConfig(encodedConfig string, overrideAddress string) error {
	rc, err := clusterworker.DecodeRemoteWorkerConfig(encodedConfig)
	if err != nil {
		return fmt.Errorf("decode worker config: %w", err)
	}
	if rc.WorkerID == "" || rc.Listen == "" {
		return fmt.Errorf("worker config missing required fields (workerId, listen)")
	}
	if rc.WorkerID == "local" {
		return fmt.Errorf("cannot import the local worker")
	}
	ctx := context.Background()

	// Determine the dial address.  Prefer the override supplied by the
	// employer (the real IP:port of the employee's machine); fall back to
	// the listen address embedded in the config.
	address := rc.Listen
	if overrideAddress != "" {
		if !strings.Contains(overrideAddress, ":") {
			if _, port, portErr := net.SplitHostPort(rc.Listen); portErr == nil {
				overrideAddress = net.JoinHostPort(overrideAddress, port)
			}
		}
		address = overrideAddress
	}

	// Build TLS config.
	var tlsConfig domain.WorkerTLSConfig
	if rc.EmployerCertPEM != "" && rc.EmployerKeyPEM != "" {
		tlsConfig = domain.WorkerTLSConfig{
			CACertPEM:     []byte(rc.CACertPEM),
			ClientCertPEM: []byte(rc.EmployerCertPEM),
			ClientKeyPEM:  []byte(rc.EmployerKeyPEM),
		}
	} else {
		stored, tlsErr := s.repository.WorkerTLS(ctx, rc.WorkerID)
		if tlsErr != nil {
			return fmt.Errorf("no TLS credentials found for worker %q — the encoded config is too old (missing employer fields); run 'Generate Remote Worker Config' first to create a new config, or add the worker manually with CA/client cert/key", rc.WorkerID)
		}
		tlsConfig = stored
	}

	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = rc.WorkerID
	}

	if err := s.client.SetTLSFromConfig(rc.WorkerID, tlsConfig); err != nil {
		return fmt.Errorf("apply TLS config: %w", err)
	}

	node := domain.WorkerNode{
		ID:               rc.WorkerID,
		Address:          address,
		Type:             domain.WorkerTypeRemote,
		Version:          rc.Version,
		Enabled:          true,
		SkipVersionCheck: true, // user explicitly acknowledged version mismatch
		TLSServerName:    tlsConfig.ServerName,
	}
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	if err := s.repository.PutWorkerTLS(ctx, rc.WorkerID, tlsConfig); err != nil {
		return err
	}
	if err := s.refreshResources(ctx); err != nil {
		return err
	}

	// Force health check — bypass version check.
	healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.client.HealthForce(healthCtx, node); err != nil {
		log.Printf("[cluster] health check (force) for imported worker %s (%s): %v", node.ID, node.Address, err)
		// Note: even if the health check fails, the worker is persisted
		// with SkipVersionCheck=true — next time refreshResources calls
		// Health(), it will automatically skip the version check.
	} else {
		log.Printf("[cluster] worker %s connected via force (%s, skipVersionCheck=true)", node.ID, node.Address)
	}
	return nil
}
