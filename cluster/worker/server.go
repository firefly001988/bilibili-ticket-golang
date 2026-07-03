package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/executor"
	pb "bilibili-ticket-golang/cluster/worker/proto"
	biliclock "bilibili-ticket-golang/lib/biliutils/clock"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Config struct {
	Listen           string        `json:"listen"`
	DataDir          string        `json:"dataDir"`
	PollInterval     time.Duration `json:"-"`
	PollIntervalSec  int           `json:"pollIntervalSec"`
	LeaseDuration    time.Duration `json:"-"`
	LeaseDurationSec int           `json:"leaseDurationSec"`
	WorkerID         string        `json:"workerId"`
	Version          string        `json:"version"`
	PluginVersion    string        `json:"pluginVersion,omitempty"`
	AlgorithmVersion string        `json:"algorithmVersion,omitempty"`
	PluginDir        string        `json:"pluginDir,omitempty"`
	CaptchaPlugin    string        `json:"captchaPlugin,omitempty"`
	CalibrateClock   bool          `json:"calibrateClock,omitempty"`
	// TLS fields – if empty, Normalize() auto‑generates a local CA + server cert
	// and loads them from disk.  When non‑empty they are serialised directly so
	// a single worker.json can carry all the material a remote worker needs.
	CACertPEM     []byte `json:"caCertPEM,omitempty"`
	ServerCertPEM []byte `json:"serverCertPEM,omitempty"`
	ServerKeyPEM  []byte `json:"serverKeyPEM,omitempty"`
}

func (c *Config) Normalize() error {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:37900"
	}
	if c.DataDir == "" {
		c.DataDir = "data/worker"
	}
	if c.PollInterval == 0 {
		c.PollInterval = time.Duration(c.PollIntervalSec) * time.Second
	}
	if c.PollInterval == 0 {
		c.PollInterval = 15 * time.Second
	}
	if c.PollInterval < 10*time.Second || c.PollInterval > 60*time.Second {
		return errors.New("poll interval must be between 10 and 60 seconds")
	}
	if c.LeaseDuration == 0 {
		c.LeaseDuration = time.Duration(c.LeaseDurationSec) * time.Second
	}
	minimum := 3 * c.PollInterval
	if minimum < 180*time.Second {
		minimum = 180 * time.Second
	}
	if c.LeaseDuration < minimum {
		c.LeaseDuration = minimum
	}
	// Auto‑generate TLS if not provided.
	if len(c.CACertPEM) == 0 || len(c.ServerCertPEM) == 0 || len(c.ServerKeyPEM) == 0 {
		if err := os.MkdirAll(c.DataDir, 0700); err != nil {
			return err
		}
		bundle, _, err := LoadOrGenerateLocalTLS(c.DataDir)
		if err != nil {
			return fmt.Errorf("auto‑generate TLS: %w", err)
		}
		caPEM, certPEM, keyPEM, err := LoadLocalServerTLS(c.DataDir)
		if err != nil {
			return fmt.Errorf("load server TLS: %w", err)
		}
		c.CACertPEM, c.ServerCertPEM, c.ServerKeyPEM = caPEM, certPEM, keyPEM
		_ = bundle
	}
	return nil
}

type BackendFactory func(domain.ExecutionSpec) (executor.Backend, error)

type task struct {
	spec          domain.ExecutionSpec
	specHash      string
	state         domain.AttemptState
	result        domain.ExecutionResult
	leaseUntil    time.Time
	cooldownUntil time.Time // zero when not cooling
	cancel        context.CancelFunc
	logs          []LogEntry
	logSeq        int64
}

type LogEntry struct {
	Sequence  int64     `json:"sequence"`
	Time      time.Time `json:"time"`
	Stage     string    `json:"stage"`
	Message   string    `json:"message"`
	Code      int       `json:"code,omitempty"`
	Retryable bool      `json:"retryable,omitempty"`
}

type Server struct {
	config            Config
	factory           BackendFactory
	store             *SuccessStore
	mu                sync.Mutex
	tasks             map[string]*task
	active            string
	now               func() time.Time
	completedNotifier func(t *task) // called when a task completes to push result over heartbeat
	grpcServer        *grpc.Server  // set by ServeOn/ListenAndServe; nil until serving

	// Global configuration pushed by the employer via Configure RPC.
	globalConfig   GlobalConfig
	globalConfigMu sync.RWMutex

	// Cached clock offsets (computed with TTL, reported in Health).
	biliOffset   time.Duration
	ntpOffset    time.Duration
	offsetsReady bool
	offsetsAt    time.Time
	offsetsMu    sync.Mutex
}

// GlobalConfig holds runtime configuration pushed by the employer.
type GlobalConfig struct {
	RetryIntervalMs int64 `json:"retryIntervalMs"`
	StartDelayMs    int64 `json:"startDelayMs"`
}

func NewServer(config Config, factory BackendFactory) (*Server, error) {
	if err := config.Normalize(); err != nil {
		return nil, err
	}
	store, err := OpenSuccessStore(filepath.Join(config.DataDir, "success-orders.jsonl"))
	if err != nil {
		return nil, err
	}
	if factory == nil {
		factory = func(spec domain.ExecutionSpec) (executor.Backend, error) {
			return executor.NewBilibiliBackend(spec.Credentials)
		}
	}
	s := &Server{config: config, factory: factory, store: store, tasks: make(map[string]*task), now: time.Now}
	for id, result := range store.All() {
		s.tasks[id] = &task{spec: domain.ExecutionSpec{AttemptID: id, IntentID: result.IntentID}, specHash: result.SpecHash, state: domain.AttemptSucceeded, result: result}
	}
	go s.reapLeases()
	return s, nil
}

// NewGRPCService returns a gRPC WorkerServiceServer backed by the given Server.
// This is exported for testing and for callers that manage their own gRPC server.
func NewGRPCService(s *Server) pb.WorkerServiceServer {
	return &workerService{server: s}
}

// Stop immediately terminates the gRPC server and all active connections.
// Safe to call multiple times; no-op if not serving.
func (s *Server) Stop() {
	s.mu.Lock()
	gs := s.grpcServer
	s.grpcServer = nil
	// Cancel the currently active task if any.
	if s.active != "" {
		if t := s.tasks[s.active]; t != nil && t.cancel != nil {
			t.cancel()
		}
	}
	s.mu.Unlock()
	if gs != nil {
		gs.Stop()
	}
}

func (s *Server) ListenAndServe() error {
	tlsCfg, err := NewServerTLSConfig(s.config.CACertPEM, s.config.ServerCertPEM, s.config.ServerKeyPEM)
	if err != nil {
		return fmt.Errorf("build server TLS config: %w", err)
	}
	lis, err := net.Listen("tcp", s.config.Listen)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	pb.RegisterWorkerServiceServer(grpcServer, &workerService{server: s})
	s.mu.Lock()
	s.grpcServer = grpcServer
	s.mu.Unlock()
	return grpcServer.Serve(lis)
}

// ServeOn serves the gRPC worker on a pre-existing listener. The caller is
// responsible for closing the listener to initiate a graceful shutdown.
func (s *Server) ServeOn(lis net.Listener) error {
	tlsCfg, err := NewServerTLSConfig(s.config.CACertPEM, s.config.ServerCertPEM, s.config.ServerKeyPEM)
	if err != nil {
		return fmt.Errorf("build server TLS config: %w", err)
	}
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	pb.RegisterWorkerServiceServer(grpcServer, &workerService{server: s})
	s.mu.Lock()
	s.grpcServer = grpcServer
	s.mu.Unlock()
	return grpcServer.Serve(lis)
}

// ---------------------------------------------------------------------------
// gRPC service implementation (thin adapter over Server)
// ---------------------------------------------------------------------------

type workerService struct {
	pb.UnimplementedWorkerServiceServer
	server *Server
}

// =============================================================================
// Health
// =============================================================================

func (ws *workerService) Health(_ context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	s := ws.server
	employerVer := req.GetEmployerVersion()
	workerVer := s.config.Version
	versionOK := employerVer == "" || employerVer == workerVer
	if employerVer != "" && employerVer != workerVer {
		log.Printf("[worker] Health version MISMATCH (employer=%q worker=%q)", employerVer, workerVer)
	} else if employerVer != "" {
		log.Printf("[worker] Health version OK (both=%q, worker=%s)", employerVer, s.config.WorkerID)
	} else {
		log.Printf("[worker] Health version skipped (employer version empty, worker=%s)", s.config.WorkerID)
	}
	biliMs, ntpMs := s.clockOffsets()
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	return &pb.HealthResponse{
		WorkerId:          s.config.WorkerID,
		Version:           workerVer,
		PluginVersion:     s.config.PluginVersion,
		AlgorithmVersion:  s.config.AlgorithmVersion,
		CaptchaPlugin:     s.config.CaptchaPlugin,
		ClockCalibration:  s.config.CalibrateClock,
		ActiveAttemptId:   active,
		ProtocolVersionOk: versionOK,
		BilibiliOffsetMs:  biliMs,
		NtpOffsetMs:       ntpMs,
	}, nil
}

const clockOffsetCacheTTL = 120 * time.Second

// clockOffsets computes (or returns cached) Bilibili API and NTP clock
// offsets for this worker.  Results are cached for clockOffsetCacheTTL
// (120s) before re-measuring, so that temporary network failures at
// startup are eventually self-healing.
func (s *Server) clockOffsets() (biliMs, ntpMs int64) {
	s.offsetsMu.Lock()
	defer s.offsetsMu.Unlock()

	// Return cached values if they are fresh enough.
	if s.offsetsReady && time.Since(s.offsetsAt) < clockOffsetCacheTTL {
		return s.biliOffset.Milliseconds(), s.ntpOffset.Milliseconds()
	}

	if s.config.CalibrateClock {
		if off, err := biliclock.GetBilibiliClockOffset(); err == nil {
			s.biliOffset = off
		}
		if off, err := biliclock.GetNTPClockOffset("ntp.aliyun.com"); err == nil {
			s.ntpOffset = off
		}
	}
	s.offsetsReady = true
	s.offsetsAt = time.Now()
	return s.biliOffset.Milliseconds(), s.ntpOffset.Milliseconds()
}

// =============================================================================
// Submit
// =============================================================================

func (ws *workerService) Submit(_ context.Context, req *pb.SubmitRequest) (*pb.SubmitResponse, error) {
	s := ws.server
	spec, err := specFromProto(req.Spec)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := spec.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	hash := spec.Hash()

	s.mu.Lock()
	if existing, ok := s.tasks[spec.AttemptID]; ok {
		if existing.specHash != hash {
			s.mu.Unlock()
			return nil, status.Error(codes.AlreadyExists, "attemptId already exists with a different spec")
		}
		resp := &pb.SubmitResponse{Status: statusToProto(s.snapshot(existing))}
		s.mu.Unlock()
		return resp, nil
	}
	// For non-BWS tasks: only one active task at a time.
	// BWS tasks are scheduled independently and can coexist.
	if s.active != "" && spec.TaskType != domain.TaskTypeBWS {
		s.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "worker is busy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &task{spec: spec, specHash: hash, state: domain.AttemptWaiting, leaseUntil: s.now().Add(s.config.LeaseDuration), cancel: cancel}
	s.tasks[spec.AttemptID], s.active = t, spec.AttemptID
	resp := &pb.SubmitResponse{Status: statusToProto(s.snapshot(t))}
	s.mu.Unlock()
	s.logTask(t, "accepted", "task accepted for intent "+spec.IntentID, 0, false)
	go s.run(ctx, t)
	return resp, nil
}

// =============================================================================
// Status
// =============================================================================

func (ws *workerService) Status(_ context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	s := ws.server
	s.mu.Lock()
	t, ok := s.tasks[req.AttemptId]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "attempt not found")
	}
	if !t.state.Terminal() {
		t.leaseUntil = s.now().Add(s.config.LeaseDuration)
	}
	resp := &pb.StatusResponse{Status: statusToProto(s.snapshot(t))}
	s.mu.Unlock()
	return resp, nil
}

// =============================================================================
// Logs
// =============================================================================

func (ws *workerService) Logs(_ context.Context, req *pb.LogsRequest) (*pb.LogsResponse, error) {
	s := ws.server
	s.mu.Lock()
	t, ok := s.tasks[req.AttemptId]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "attempt not found")
	}
	entries := make([]*pb.LogEntry, len(t.logs))
	for i, e := range t.logs {
		entries[i] = logEntryToProto(e)
	}
	s.mu.Unlock()
	return &pb.LogsResponse{Entries: entries}, nil
}

// =============================================================================
// Stop
// =============================================================================

func (ws *workerService) Stop(_ context.Context, req *pb.StopRequest) (*pb.StopResponse, error) {
	s := ws.server
	s.mu.Lock()
	t, ok := s.tasks[req.AttemptId]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "attempt not found")
	}
	if !t.state.Terminal() {
		t.state = domain.AttemptStopping
		t.cancel()
	}
	resp := &pb.StopResponse{Status: statusToProto(s.snapshot(t))}
	s.mu.Unlock()
	return resp, nil
}

// =============================================================================
// Ack
// =============================================================================

func (ws *workerService) Ack(_ context.Context, req *pb.AckRequest) (*pb.AckResponse, error) {
	s := ws.server
	id := req.AttemptId
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "attempt not found")
	}
	if !t.state.Terminal() {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "attempt is not terminal")
	}
	if !t.result.Success {
		delete(s.tasks, id)
	}
	s.mu.Unlock()
	return &pb.AckResponse{}, nil
}

// =============================================================================
// Configure – receive global settings from the employer
// =============================================================================

func (ws *workerService) Configure(_ context.Context, req *pb.ConfigureRequest) (*pb.ConfigureResponse, error) {
	s := ws.server
	cfg := req.GetConfig()
	if cfg == nil {
		return nil, status.Error(codes.InvalidArgument, "config is required")
	}
	s.globalConfigMu.Lock()
	s.globalConfig = GlobalConfig{
		RetryIntervalMs: cfg.RetryIntervalMs,
		StartDelayMs:    cfg.StartDelayMs,
	}
	s.globalConfigMu.Unlock()
	log.Printf("[worker] global config updated: retryInterval=%dms startDelay=%dms", cfg.RetryIntervalMs, cfg.StartDelayMs)
	return &pb.ConfigureResponse{}, nil
}

// =============================================================================
// ListBuyers – fetch all real-name buyers from a Bilibili account
// =============================================================================

func (ws *workerService) ListBuyers(_ context.Context, req *pb.ListBuyersRequest) (*pb.ListBuyersResponse, error) {
	if req.Credentials == nil {
		return nil, status.Error(codes.InvalidArgument, "credentials are required")
	}
	creds := credentialsFromProto(req.Credentials)
	backend, err := executor.NewBilibiliBackend(creds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "initialize Bilibili client: %v", err)
	}
	client, _ := backend.ClientAndJar()
	errVal, list := client.GetRealnameBuyerListNew()
	if errVal != nil {
		return nil, status.Errorf(codes.Internal, "list buyers: %v", errVal)
	}
	buyers := make([]*pb.Buyer, 0, len(list))
	for i, item := range list {
		b := &pb.Buyer{
			BuyerId: item.Id,
			Name:    item.Name,
			Tel:     item.Tel,
			IdCard:  item.IdCard,
			Type:    int32(item.IdType),
		}
		// Fetch full unmasked sensitive data for each buyer.
		if item.Id > 0 {
			sensitiveErr, sensitive := client.GetTargetBuyerSensitiveData(item.Id)
			if sensitiveErr == nil && sensitive.PersonalId != "" {
				b.IdCard = sensitive.PersonalId
				if sensitive.Tel != "" {
					b.Tel = sensitive.Tel
				}
				if sensitive.Name != "" {
					b.Name = sensitive.Name
				}
				if sensitive.IdType != 0 {
					b.Type = int32(sensitive.IdType)
				}
			}
		}
		buyers = append(buyers, b)
		// Pause every 8 buyers to avoid rate‑limiting.
		if (i+1)%8 == 0 && i+1 < len(list) {
			time.Sleep(50 * time.Millisecond)
		}
	}
	refreshed := backend.Credentials()
	return &pb.ListBuyersResponse{
		Buyers:      buyers,
		Credentials: credentialsToProto(refreshed),
	}, nil
}

// =============================================================================
// ListBuyersMasked – fetch buyer list without unmasking sensitive data
// =============================================================================

func (ws *workerService) ListBuyersMasked(_ context.Context, req *pb.ListBuyersRequest) (*pb.ListBuyersResponse, error) {
	if req.Credentials == nil {
		return nil, status.Error(codes.InvalidArgument, "credentials are required")
	}
	creds := credentialsFromProto(req.Credentials)
	backend, err := executor.NewBilibiliBackend(creds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "initialize Bilibili client: %v", err)
	}
	client, _ := backend.ClientAndJar()
	errVal, list := client.GetRealnameBuyerListNew()
	if errVal != nil {
		return nil, status.Errorf(codes.Internal, "list buyers: %v", errVal)
	}
	buyers := make([]*pb.Buyer, 0, len(list))
	for i, item := range list {
		b := &pb.Buyer{
			BuyerId: item.Id,
			Name:    item.Name,
			Tel:     item.Tel,
			IdCard:  item.IdCard,
			Type:    int32(item.IdType),
		}
		buyers = append(buyers, b)
		// Pause every 8 buyers to avoid rate‑limiting.
		if (i+1)%8 == 0 && i+1 < len(list) {
			time.Sleep(50 * time.Millisecond)
		}
	}
	refreshed := backend.Credentials()
	return &pb.ListBuyersResponse{
		Buyers:      buyers,
		Credentials: credentialsToProto(refreshed),
	}, nil
}

// =============================================================================
// CreateBuyer – create a new real-name buyer on the target Bilibili account
// =============================================================================

func (ws *workerService) CreateBuyer(_ context.Context, req *pb.CreateBuyerRequest) (*pb.CreateBuyerResponse, error) {
	if req.Credentials == nil {
		return nil, status.Error(codes.InvalidArgument, "credentials are required")
	}
	if req.Buyer == nil {
		return nil, status.Error(codes.InvalidArgument, "buyer is required")
	}
	creds := credentialsFromProto(req.Credentials)
	backend, err := executor.NewBilibiliBackend(creds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "initialize Bilibili client: %v", err)
	}
	client, _ := backend.ClientAndJar()
	b := req.Buyer
	if errVal := client.CreateBuyer(b.Name, b.Tel, int(b.Type), b.IdCard, false); errVal != nil {
		return nil, status.Errorf(codes.Internal, "create buyer: %v", errVal)
	}
	// Re-list to get the newly created buyer's ID.
	errVal, list := client.GetRealnameBuyerListNew()
	if errVal != nil {
		return nil, status.Errorf(codes.Internal, "list buyers after create: %v", errVal)
	}
	var created *pb.Buyer
	for _, item := range list {
		if item.Name == b.Name && item.Tel == b.Tel {
			created = &pb.Buyer{
				BuyerId: item.Id,
				Name:    item.Name,
				Tel:     item.Tel,
				IdCard:  item.IdCard,
				Type:    int32(item.IdType),
			}
			// Fetch full unmasked data.
			if item.Id > 0 {
				sensitiveErr, sensitive := client.GetTargetBuyerSensitiveData(item.Id)
				if sensitiveErr == nil && sensitive.PersonalId != "" {
					created.IdCard = sensitive.PersonalId
					if sensitive.Tel != "" {
						created.Tel = sensitive.Tel
					}
					if sensitive.Name != "" {
						created.Name = sensitive.Name
					}
					if sensitive.IdType != 0 {
						created.Type = int32(sensitive.IdType)
					}
				}
			}
			break
		}
	}
	if created == nil {
		return nil, status.Error(codes.Internal, "created buyer was not returned by API")
	}
	refreshed := backend.Credentials()
	return &pb.CreateBuyerResponse{
		Buyer:       created,
		Credentials: credentialsToProto(refreshed),
	}, nil
}

// =============================================================================
// GetBuyerSensitiveData – fetch unmasked buyer details
// =============================================================================

func (ws *workerService) GetBuyerSensitiveData(_ context.Context, req *pb.GetBuyerSensitiveDataRequest) (*pb.GetBuyerSensitiveDataResponse, error) {
	if req.Credentials == nil {
		return nil, status.Error(codes.InvalidArgument, "credentials are required")
	}
	if req.BuyerId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "buyer_id is required")
	}
	creds := credentialsFromProto(req.Credentials)
	backend, err := executor.NewBilibiliBackend(creds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "initialize Bilibili client: %v", err)
	}
	client, _ := backend.ClientAndJar()
	sensitiveErr, sensitive := client.GetTargetBuyerSensitiveData(req.BuyerId)
	if sensitiveErr != nil {
		return nil, status.Errorf(codes.Internal, "get buyer sensitive data: %v", sensitiveErr)
	}
	return &pb.GetBuyerSensitiveDataResponse{
		Buyer: &pb.Buyer{
			BuyerId: req.BuyerId,
			Name:    sensitive.Name,
			Tel:     sensitive.Tel,
			IdCard:  sensitive.PersonalId,
			Type:    int32(sensitive.IdType),
		},
	}, nil
}

// =============================================================================
// DeleteBuyer – remove a real-name buyer from a Bilibili account
// =============================================================================

func (ws *workerService) DeleteBuyer(_ context.Context, req *pb.DeleteBuyerRequest) (*pb.DeleteBuyerResponse, error) {
	if req.Credentials == nil {
		return nil, status.Error(codes.InvalidArgument, "credentials are required")
	}
	if req.BuyerId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "buyer_id is required")
	}
	creds := credentialsFromProto(req.Credentials)
	backend, err := executor.NewBilibiliBackend(creds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "initialize Bilibili client: %v", err)
	}
	client, _ := backend.ClientAndJar()
	if errVal := client.DeleteTargetBuyer(req.BuyerId); errVal != nil {
		return nil, status.Errorf(codes.Internal, "delete buyer: %v", errVal)
	}
	return &pb.DeleteBuyerResponse{}, nil
}

// =============================================================================
// CheckBWSBind – check whether an account has a BWS electronic ticket bound
// =============================================================================

func (ws *workerService) CheckBWSBind(_ context.Context, req *pb.CheckBWSBindRequest) (*pb.CheckBWSBindResponse, error) {
	if req.Credentials == nil {
		return nil, status.Error(codes.InvalidArgument, "credentials are required")
	}
	creds := credentialsFromProto(req.Credentials)
	backend, err := executor.NewBilibiliBackend(creds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "initialize Bilibili client: %v", err)
	}
	client, _ := backend.ClientAndJar()
	isBind, err := client.CheckBWSBindStatus()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check BWS bind: %v", err)
	}
	refreshed := backend.Credentials()
	return &pb.CheckBWSBindResponse{
		IsBind:      isBind,
		Credentials: credentialsToProto(refreshed),
	}, nil
}

// =============================================================================
// GetBWSReservationInfo – fetch BWS activity info for the given dates
// =============================================================================

func (ws *workerService) GetBWSReservationInfo(_ context.Context, req *pb.BWSReservationInfoRequest) (*pb.BWSReservationInfoResponse, error) {
	if req.Credentials == nil {
		return nil, status.Error(codes.InvalidArgument, "credentials are required")
	}
	if req.ReserveDates == "" {
		return nil, status.Error(codes.InvalidArgument, "reserve_dates is required")
	}
	creds := credentialsFromProto(req.Credentials)
	backend, err := executor.NewBilibiliBackend(creds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "initialize Bilibili client: %v", err)
	}
	client, _ := backend.ClientAndJar()
	data, err := client.GetBWSReservationInfo(req.ReserveDates, int(req.ReserveType))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get BWS reservation info: %v", err)
	}

	// Build activities list
	activities := make([]*pb.BWSActivity, 0)
	for _, act := range data.ActivityMapping {
		activities = append(activities, &pb.BWSActivity{
			ReserveId:        int32(act.ReserveID),
			ActTitle:         act.ActTitle,
			ReserveBeginTime: act.ReserveBeginTime,
			ActBeginTime:     act.ActBeginTime,
			State:            int32(act.State),
			DescribeInfo:     act.DescribeInfo,
			ReserveDate:      act.ReserveDate,
		})
	}

	// Build ticket info list
	ticketInfos := make([]*pb.BWSTicketInfo, 0)
	for date, ti := range data.TicketInfo {
		ticketInfos = append(ticketInfos, &pb.BWSTicketInfo{
			Date:       date,
			Ticket:     ti.Ticket,
			ScreenName: ti.ScreenName,
			SkuName:    ti.SkuName,
		})
	}

	refreshed := backend.Credentials()
	return &pb.BWSReservationInfoResponse{
		Activities:  activities,
		TicketInfos: ticketInfos,
		ReservedIds: func() map[int32]bool {
			m := make(map[int32]bool)
			for id := range data.ReservedIDs {
				m[int32(id)] = true
			}
			return m
		}(),
		Credentials: credentialsToProto(refreshed),
	}, nil
}

// =============================================================================
// BindBWSTicket – bind real‑name identity to a BWS electronic ticket
// =============================================================================

func (ws *workerService) BindBWSTicket(_ context.Context, req *pb.BindBWSTicketRequest) (*pb.BindBWSTicketResponse, error) {
	if req.Credentials == nil {
		return nil, status.Error(codes.InvalidArgument, "credentials are required")
	}
	if req.TicketNo == "" || req.PersonalId == "" || req.UserName == "" {
		return nil, status.Error(codes.InvalidArgument, "ticket_no, personal_id, and user_name are required")
	}
	creds := credentialsFromProto(req.Credentials)
	backend, err := executor.NewBilibiliBackend(creds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "initialize Bilibili client: %v", err)
	}
	client, _ := backend.ClientAndJar()
	code, message, err := client.BindBWSTicket(int(req.Bid), int(req.IdType), req.PersonalId, req.TicketNo, req.UserName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "bind BWS ticket: %v", err)
	}
	refreshed := backend.Credentials()
	return &pb.BindBWSTicketResponse{
		Code:        int32(code),
		Message:     message,
		Credentials: credentialsToProto(refreshed),
	}, nil
}

// Heartbeat
// =============================================================================

const heartbeatInterval = 5 * time.Second

func (ws *workerService) Heartbeat(stream pb.WorkerService_HeartbeatServer) error {
	s := ws.server
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	// Register a notifier so complete() can push finished tasks immediately.
	s.completedNotifier = func(t *task) {
		msg := &pb.HeartbeatMsg{
			WorkerId:        s.config.WorkerID,
			ActiveAttemptId: "",
			Sequence:        0,
			Time:            timestamppb.New(s.now()),
			CompletedTask:   executionResultToProto(t.result),
		}
		_ = stream.Send(msg)
	}
	defer func() { s.completedNotifier = nil }()

	// Send heartbeats to the master.
	errCh := make(chan error, 1)
	go func() {
		defer cancel()
		seq := int64(0)
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				active := s.active
				s.mu.Unlock()
				seq++
				msg := &pb.HeartbeatMsg{
					WorkerId:        s.config.WorkerID,
					ActiveAttemptId: active,
					Sequence:        seq,
					Time:            timestamppb.New(s.now()),
				}
				if err := stream.Send(msg); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	// Read echo messages (acknowledgements) to keep the stream alive.
	go func() {
		defer cancel()
		for {
			_, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	// Block until either side closes.
	err := <-errCh
	return err
}

// ---------------------------------------------------------------------------
// Proto ↔ domain conversions
// ---------------------------------------------------------------------------

func specFromProto(p *pb.ExecutionSpec) (domain.ExecutionSpec, error) {
	if p == nil {
		return domain.ExecutionSpec{}, errors.New("nil ExecutionSpec")
	}
	s := domain.ExecutionSpec{
		AttemptID:    p.AttemptId,
		IntentID:     p.IntentId,
		ProjectID:    p.ProjectId,
		ScreenID:     p.ScreenId,
		SKUID:        p.SkuId,
		StartMode:    startModeFromProto(p.StartMode),
		IntervalMS:   p.IntervalMs,
		StartDelayMS: p.StartDelayMs,
		Credentials:  credentialsFromProto(p.Credentials),
		TaskType:     taskTypeFromProto(p.TaskType),
		// BWS fields
		BWSActivityID:    int(p.BwsActivityId),
		BWSTicketNo:      p.BwsTicketNo,
		BWSActivityTitle: p.BwsActivityTitle,
		BWSReserveTime:   p.BwsReserveTime,
		BWSReserveDate:   p.BwsReserveDate,
	}
	if p.StartAt != nil {
		s.StartAt = p.StartAt.AsTime()
	}
	if p.Deadline != nil {
		s.Deadline = p.Deadline.AsTime()
	}
	for _, buyer := range p.Buyers {
		s.Buyers = append(s.Buyers, buyerFromProto(buyer))
	}
	return s, nil
}

func buyerFromProto(p *pb.Buyer) domain.Buyer {
	if p == nil {
		return domain.Buyer{}
	}
	return domain.Buyer{
		LogicalID: p.LogicalId,
		BuyerID:   p.BuyerId,
		Name:      p.Name,
		Tel:       p.Tel,
		IDCard:    p.IdCard,
		Type:      int(p.Type),
	}
}

func buyerToProto(b domain.Buyer) *pb.Buyer {
	return &pb.Buyer{
		LogicalId: b.LogicalID,
		BuyerId:   b.BuyerID,
		Name:      b.Name,
		Tel:       b.Tel,
		IdCard:    b.IDCard,
		Type:      int32(b.Type),
	}
}

func credentialsFromProto(p *pb.Credentials) domain.Credentials {
	if p == nil {
		return domain.Credentials{}
	}
	c := domain.Credentials{
		Cookies:      p.Cookies,
		RefreshToken: p.RefreshToken,
		Version:      p.Version,
	}
	if len(p.DeviceProfile) > 0 {
		c.DeviceProfile = p.DeviceProfile
	}
	for _, hc := range p.CookieJar {
		c.CookieJar = append(c.CookieJar, domain.HTTPCookie{
			Name:     hc.Name,
			Value:    hc.Value,
			Domain:   hc.Domain,
			Path:     hc.Path,
			Secure:   hc.Secure,
			HTTPOnly: hc.HttpOnly,
			Expires:  hc.Expires,
		})
	}
	return c
}

func credentialsToProto(c domain.Credentials) *pb.Credentials {
	p := &pb.Credentials{
		Cookies:      c.Cookies,
		RefreshToken: c.RefreshToken,
		Version:      c.Version,
	}
	if len(c.DeviceProfile) > 0 {
		p.DeviceProfile = c.DeviceProfile
	}
	for _, hc := range c.CookieJar {
		p.CookieJar = append(p.CookieJar, &pb.HTTPCookie{
			Name:     hc.Name,
			Value:    hc.Value,
			Domain:   hc.Domain,
			Path:     hc.Path,
			Secure:   hc.Secure,
			HttpOnly: hc.HTTPOnly,
			Expires:  hc.Expires,
		})
	}
	return p
}

func startModeFromProto(m pb.StartMode) domain.StartMode {
	switch m {
	case pb.StartMode_START_SCHEDULED:
		return domain.StartScheduled
	default:
		return domain.StartImmediate
	}
}

func taskTypeFromProto(t pb.TaskType) domain.TaskType {
	switch t {
	case pb.TaskType_TASK_TYPE_BWS:
		return domain.TaskTypeBWS
	default:
		return domain.TaskTypeTicket
	}
}

func statusToProto(st Status) *pb.TaskStatus {
	ts := &pb.TaskStatus{
		AttemptId: st.AttemptID,
		SpecHash:  st.SpecHash,
		State:     attemptStateToProto(st.State),
		Result:    executionResultToProto(st.Result),
	}
	if !st.LeaseUntil.IsZero() {
		ts.LeaseUntil = timestamppb.New(st.LeaseUntil)
	}
	return ts
}

func attemptStateToProto(s domain.AttemptState) pb.AttemptState {
	switch s {
	case domain.AttemptQueued:
		return pb.AttemptState_ATTEMPT_QUEUED
	case domain.AttemptWaiting:
		return pb.AttemptState_ATTEMPT_WAITING
	case domain.AttemptRunning:
		return pb.AttemptState_ATTEMPT_RUNNING
	case domain.AttemptStopping:
		return pb.AttemptState_ATTEMPT_STOPPING
	case domain.AttemptStopped:
		return pb.AttemptState_ATTEMPT_STOPPED
	case domain.AttemptSucceeded:
		return pb.AttemptState_ATTEMPT_SUCCEEDED
	case domain.AttemptFailed:
		return pb.AttemptState_ATTEMPT_FAILED
	case domain.AttemptCooldown:
		return pb.AttemptState_ATTEMPT_COOLDOWN
	default:
		return pb.AttemptState_ATTEMPT_QUEUED
	}
}

func executionResultToProto(r domain.ExecutionResult) *pb.ExecutionResult {
	er := &pb.ExecutionResult{
		AttemptId:     r.AttemptID,
		IntentId:      r.IntentID,
		SpecHash:      r.SpecHash,
		State:         attemptStateToProto(r.State),
		Success:       r.Success,
		OrderId:       r.OrderID,
		Reason:        failureReasonToProto(r.Reason),
		Message:       r.Message,
		Retryable:     r.Retryable,
		PaymentUrl:    r.PaymentURL,
		PaymentExpire: r.PaymentExpire,
		OrderTime:     r.OrderTime,
		Credentials: &pb.Credentials{
			Cookies:       r.Credentials.Cookies,
			RefreshToken:  r.Credentials.RefreshToken,
			Version:       r.Credentials.Version,
			DeviceProfile: r.Credentials.DeviceProfile,
		},
	}
	if len(r.Credentials.CookieJar) > 0 {
		for _, hc := range r.Credentials.CookieJar {
			er.Credentials.CookieJar = append(er.Credentials.CookieJar, &pb.HTTPCookie{
				Name:     hc.Name,
				Value:    hc.Value,
				Domain:   hc.Domain,
				Path:     hc.Path,
				Secure:   hc.Secure,
				HttpOnly: hc.HTTPOnly,
				Expires:  hc.Expires,
			})
		}
	}
	if !r.StartedAt.IsZero() {
		er.StartedAt = timestamppb.New(r.StartedAt)
	}
	if !r.FinishedAt.IsZero() {
		er.FinishedAt = timestamppb.New(r.FinishedAt)
	}
	return er
}

func failureReasonToProto(r domain.FailureReason) pb.FailureReason {
	switch r {
	case domain.FailureDeadline:
		return pb.FailureReason_FAILURE_DEADLINE
	case domain.FailureStopped:
		return pb.FailureReason_FAILURE_STOPPED
	case domain.FailureCookieInvalid:
		return pb.FailureReason_FAILURE_COOKIE_INVALID
	case domain.FailureHTTP412:
		return pb.FailureReason_FAILURE_HTTP_412
	case domain.FailureCaptcha:
		return pb.FailureReason_FAILURE_CAPTCHA
	case domain.FailureAccountRisk:
		return pb.FailureReason_FAILURE_ACCOUNT_RISK
	case domain.FailureWorkerLost:
		return pb.FailureReason_FAILURE_WORKER_LOST
	case domain.FailureUnrecoverable:
		return pb.FailureReason_FAILURE_UNRECOVERABLE
	case domain.FailureInternal:
		return pb.FailureReason_FAILURE_INTERNAL
	default:
		return pb.FailureReason_FAILURE_NONE
	}
}

func logEntryToProto(e LogEntry) *pb.LogEntry {
	return &pb.LogEntry{
		Sequence:  e.Sequence,
		Time:      timestamppb.New(e.Time),
		Stage:     e.Stage,
		Message:   e.Message,
		Code:      int32(e.Code),
		Retryable: e.Retryable,
	}
}

func (s *Server) run(ctx context.Context, t *task) {
	// Route BWS tasks to the BWS-specific execution path.
	if t.spec.TaskType == domain.TaskTypeBWS {
		s.runBWS(ctx, t)
		return
	}

	// Apply global configuration overrides from the employer.
	s.globalConfigMu.RLock()
	gcfg := s.globalConfig
	s.globalConfigMu.RUnlock()
	if gcfg.RetryIntervalMs > 0 {
		t.spec.IntervalMS = gcfg.RetryIntervalMs
	}
	if gcfg.StartDelayMs > 0 {
		t.spec.StartDelayMS = gcfg.StartDelayMs
	}

	backend, err := s.factory(t.spec)
	if err != nil {
		s.complete(t, domain.ExecutionResult{AttemptID: t.spec.AttemptID, IntentID: t.spec.IntentID, State: domain.AttemptFailed, Reason: domain.FailureInternal, Message: err.Error(), FinishedAt: s.now()})
		return
	}
	s.mu.Lock()
	if t.state == domain.AttemptWaiting {
		t.state = domain.AttemptRunning
	}
	s.mu.Unlock()
	s.logTask(t, "started", fmt.Sprintf("mode=%s deadline=%s", t.spec.StartMode, t.spec.Deadline.Format(time.RFC3339)), 0, false)
	var executionClock executor.Clock
	if s.config.CalibrateClock {
		if offset, err := biliclock.GetBilibiliClockOffset(); err == nil {
			executionClock = executor.OffsetClock{Offset: offset}
		} else {
			_ = WriteRedactedLog(s.config.DataDir, "clock calibration failed: "+err.Error())
		}
	}
	result := (executor.Engine{
		Backend: backend,
		Clock:   executionClock,
		Observe: func(event executor.Event) {
			s.logTask(t, event.Stage, event.Message, event.Code, event.Retryable)
			if !event.CooldownEnd.IsZero() {
				s.mu.Lock()
				t.state = domain.AttemptCooldown
				t.cooldownUntil = event.CooldownEnd
				s.mu.Unlock()
			} else if t.state == domain.AttemptCooldown {
				s.mu.Lock()
				t.state = domain.AttemptRunning
				t.cooldownUntil = time.Time{}
				s.mu.Unlock()
			}
		},
		// Dynamic retry interval — reads the global config pushed by the
		// employer via Configure RPC, so changes take effect immediately
		// for running tasks without needing to restart.
		GetRetryInterval: func() int64 {
			s.globalConfigMu.RLock()
			ms := s.globalConfig.RetryIntervalMs
			s.globalConfigMu.RUnlock()
			return ms
		},
	}).Run(ctx, t.spec)
	if result.Success {
		if err := s.store.Append(result); err != nil {
			result.Success, result.State, result.Reason, result.Message = false, domain.AttemptFailed, domain.FailureInternal, "persist success: "+err.Error()
		}
	}
	s.complete(t, result)
}

// runBWS executes a BWS (Bilibili World) reservation task using the
// Engine.Run() loop with clock calibration and outcome classification.
func (s *Server) runBWS(ctx context.Context, t *task) {
	s.globalConfigMu.RLock()
	gcfg := s.globalConfig
	s.globalConfigMu.RUnlock()
	if gcfg.StartDelayMs > 0 {
		t.spec.StartDelayMS = gcfg.StartDelayMs
	}
	if gcfg.RetryIntervalMs > 0 {
		t.spec.IntervalMS = gcfg.RetryIntervalMs
	}

	bwsBackend, err := executor.NewBWSBackend(t.spec.Credentials)
	if err != nil {
		s.complete(t, domain.ExecutionResult{
			AttemptID: t.spec.AttemptID, IntentID: t.spec.IntentID,
			State: domain.AttemptFailed, Reason: domain.FailureInternal,
			Message: err.Error(), FinishedAt: s.now(),
		})
		return
	}
	bwsBackend.SetReservation(int(t.spec.BWSActivityID), t.spec.BWSTicketNo)

	s.mu.Lock()
	if t.state == domain.AttemptWaiting {
		t.state = domain.AttemptRunning
	}
	s.mu.Unlock()
	s.logTask(t, "started", fmt.Sprintf("BWS activity=%d ticket=%s reserve=%s",
		t.spec.BWSActivityID, t.spec.BWSTicketNo,
		time.Unix(t.spec.BWSReserveTime, 0).Format(time.RFC3339)), 0, false)

	var executionClock executor.Clock
	if s.config.CalibrateClock {
		if offset, cerr := biliclock.GetBilibiliClockOffset(); cerr == nil {
			executionClock = executor.OffsetClock{Offset: offset}
		} else {
			_ = WriteRedactedLog(s.config.DataDir, "clock calibration failed: "+cerr.Error())
		}
	}

	result := (executor.Engine{
		Backend:    bwsBackend,
		Classifier: executor.BWSClassifier{},
		Clock:      executionClock,
		Observe: func(event executor.Event) {
			s.logTask(t, event.Stage, event.Message, event.Code, event.Retryable)
		},
		GetRetryInterval: func() int64 {
			s.globalConfigMu.RLock()
			ms := s.globalConfig.RetryIntervalMs
			s.globalConfigMu.RUnlock()
			return ms
		},
	}).Run(ctx, t.spec)

	// On success, the Engine propagates the OrderID from Attempt.
	// BWSBackend leaves OrderID empty; fill it here for traceability.
	if result.Success && result.OrderID == "" {
		result.OrderID = fmt.Sprintf("bws-%d", t.spec.BWSActivityID)
	}

	s.complete(t, result)
}

func (s *Server) complete(t *task, result domain.ExecutionResult) {
	s.mu.Lock()
	t.result, t.state = result, result.State
	if s.active == t.spec.AttemptID {
		s.active = ""
	}
	s.mu.Unlock()
	message := fmt.Sprintf("state=%s reason=%s order=%s", result.State, result.Reason, result.OrderID)
	if result.Message != "" {
		message += " message=" + result.Message
	}
	s.logTask(t, "completed", message, 0, false)

	// Push the result immediately via the heartbeat stream so the
	// employer can dispatch the next task without waiting for a poll.
	if s.completedNotifier != nil {
		s.completedNotifier(t)
	}
}

type Status struct {
	AttemptID  string                 `json:"attemptId"`
	SpecHash   string                 `json:"specHash"`
	State      domain.AttemptState    `json:"state"`
	LeaseUntil time.Time              `json:"leaseUntil,omitempty"`
	Result     domain.ExecutionResult `json:"result"`
}

func (s *Server) snapshot(t *task) Status {
	return Status{AttemptID: t.spec.AttemptID, SpecHash: t.specHash, State: t.state, LeaseUntil: t.leaseUntil, Result: t.result}
}

func (s *Server) logTask(t *task, stage, message string, code int, retryable bool) {
	message = redactLogLine(message)
	entry := LogEntry{Time: s.now().UTC(), Stage: stage, Message: message, Code: code, Retryable: retryable}
	s.mu.Lock()
	t.logSeq++
	entry.Sequence = t.logSeq
	t.logs = append(t.logs, entry)
	if len(t.logs) > 500 {
		t.logs = append([]LogEntry(nil), t.logs[len(t.logs)-500:]...)
	}
	s.mu.Unlock()
	_ = WriteRedactedLog(s.config.DataDir, fmt.Sprintf("%s attempt=%s stage=%s code=%d retryable=%t message=%s", entry.Time.Format(time.RFC3339Nano), t.spec.AttemptID, stage, code, retryable, message))
}

func (s *Server) reapLeases() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		if t := s.tasks[s.active]; t != nil && !t.state.Terminal() && !t.leaseUntil.After(s.now()) {
			t.state = domain.AttemptStopping
			t.cancel()
		}
		s.mu.Unlock()
	}
}

func WriteRedactedLog(dataDir, line string) error {
	line = redactLogLine(line)
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "worker.log")
	if info, err := os.Stat(path); err == nil && info.Size() >= 5<<20 {
		for i := 2; i >= 0; i-- {
			from := path
			if i > 0 {
				from = fmt.Sprintf("%s.%d", path, i)
			}
			to := fmt.Sprintf("%s.%d", path, i+1)
			_ = os.Rename(from, to)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}

func redactLogLine(line string) string {
	for _, marker := range []string{"SESSDATA", "bili_jct", "refresh_token", "refreshToken"} {
		if i := strings.Index(line, marker); i >= 0 {
			line = line[:i] + marker + "=[REDACTED]"
		}
	}
	return line
}
