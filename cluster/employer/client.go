package employer

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/worker"
	pb "bilibili-ticket-golang/cluster/worker/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	heartbeatInterval = 5 * time.Second
	heartbeatTimeout  = 3 * heartbeatInterval // 15s
)

// workerConn groups a gRPC connection, its client stub, and the heartbeat state.
type workerConn struct {
	conn          *grpc.ClientConn
	client        pb.WorkerServiceClient
	lastHeartbeat time.Time
	hbCancel      context.CancelFunc // cancels the heartbeat goroutine
	mu            sync.Mutex
}

// WorkerClient manages gRPC connections to workers.
type WorkerClient struct {
	mu         sync.Mutex
	workers    map[string]*workerConn
	tlsConfigs map[string]*tls.Config
}

func NewWorkerClient() *WorkerClient {
	return &WorkerClient{
		workers:    make(map[string]*workerConn),
		tlsConfigs: make(map[string]*tls.Config),
	}
}

// SetTLS configures mTLS for a worker and closes any existing connection.
func (c *WorkerClient) SetTLS(workerID string, tlsCfg *tls.Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if wc, ok := c.workers[workerID]; ok {
		c.closeWorkerConnLocked(wc)
		delete(c.workers, workerID)
	}
	c.tlsConfigs[workerID] = tlsCfg
}

// SetTLSFromConfig builds a TLS config from WorkerTLSConfig and stores it.
func (c *WorkerClient) SetTLSFromConfig(workerID string, cfg domain.WorkerTLSConfig) error {
	tlsCfg, err := worker.NewClientTLSConfig(cfg.CACertPEM, cfg.ClientCertPEM, cfg.ClientKeyPEM, cfg.ServerName)
	if err != nil {
		return fmt.Errorf("build TLS config for worker %s: %w", workerID, err)
	}
	c.SetTLS(workerID, tlsCfg)
	return nil
}

// RemoveTLS removes a worker's TLS configuration and closes its connection.
func (c *WorkerClient) RemoveTLS(workerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if wc, ok := c.workers[workerID]; ok {
		c.closeWorkerConnLocked(wc)
		delete(c.workers, workerID)
	}
	delete(c.tlsConfigs, workerID)
}

func (c *WorkerClient) closeWorkerConnLocked(wc *workerConn) {
	if wc.hbCancel != nil {
		wc.hbCancel()
		wc.hbCancel = nil
	}
	if wc.conn != nil {
		_ = wc.conn.Close()
	}
}

// getClient returns the gRPC client for a worker, dialing and starting a
// heartbeat stream on first access.
func (c *WorkerClient) getClient(node domain.WorkerNode) (pb.WorkerServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getClientLocked(node)
}

func (c *WorkerClient) getClientLocked(node domain.WorkerNode) (pb.WorkerServiceClient, error) {
	if wc, ok := c.workers[node.ID]; ok && wc.client != nil {
		return wc.client, nil
	}
	tlsCfg, ok := c.tlsConfigs[node.ID]
	if !ok {
		return nil, fmt.Errorf("no TLS config for worker %s", node.ID)
	}
	conn, err := grpc.NewClient(
		node.Address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial worker %s at %s: %w", node.ID, node.Address, err)
	}
	cli := pb.NewWorkerServiceClient(conn)
	wc := &workerConn{
		conn:          conn,
		client:        cli,
		lastHeartbeat: time.Now(),
	}
	c.workers[node.ID] = wc

	// Start heartbeat stream.
	c.startHeartbeat(node, wc)
	return cli, nil
}

func (c *WorkerClient) startHeartbeat(node domain.WorkerNode, wc *workerConn) {
	ctx, cancel := context.WithCancel(context.Background())
	wc.hbCancel = cancel

	stream, err := wc.client.Heartbeat(ctx)
	if err != nil {
		log.Printf("[worker-client] heartbeat stream to %s failed: %v", node.ID, err)
		wc.lastHeartbeat = time.Time{} // mark dead
		return
	}

	// Read heartbeats from the worker.
	go func() {
		defer cancel()
		for {
			msg, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					log.Printf("[worker-client] heartbeat recv from %s: %v", node.ID, err)
				}
				wc.mu.Lock()
				wc.lastHeartbeat = time.Time{} // mark dead
				wc.mu.Unlock()
				return
			}
			wc.mu.Lock()
			wc.lastHeartbeat = time.Now()
			wc.mu.Unlock()

			// Echo back as acknowledgement.
			_ = stream.Send(&pb.HeartbeatMsg{
				WorkerId: msg.WorkerId,
				Sequence: msg.Sequence,
				Time:     timestamppb.Now(),
			})
		}
	}()

	// Send initial heartbeat to kick-start the stream.
	_ = stream.Send(&pb.HeartbeatMsg{WorkerId: node.ID, Sequence: 0, Time: timestamppb.Now()})
}

// isAlive returns whether the worker is currently sending heartbeats.
func (wc *workerConn) isAlive() bool {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return !wc.lastHeartbeat.IsZero() && time.Since(wc.lastHeartbeat) < heartbeatTimeout
}

// IsHealthy returns whether the worker has been seen recently via heartbeat.
func (c *WorkerClient) IsHealthy(workerID string) bool {
	c.mu.Lock()
	wc, ok := c.workers[workerID]
	c.mu.Unlock()
	if !ok {
		return false
	}
	return wc.isAlive()
}

// LastHeartbeat returns the time of the last received heartbeat (zero if unknown).
func (c *WorkerClient) LastHeartbeat(workerID string) (time.Time, bool) {
	c.mu.Lock()
	wc, ok := c.workers[workerID]
	c.mu.Unlock()
	if !ok {
		return time.Time{}, false
	}
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return wc.lastHeartbeat, !wc.lastHeartbeat.IsZero()
}

func (c *WorkerClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, wc := range c.workers {
		c.closeWorkerConnLocked(wc)
		delete(c.workers, id)
	}
}

// =========================================================================
// WorkerClient methods
// =========================================================================

func (c *WorkerClient) Submit(ctx context.Context, node domain.WorkerNode, spec domain.ExecutionSpec) error {
	cli, err := c.getClient(node)
	if err != nil {
		return err
	}
	req := &pb.SubmitRequest{Spec: specToProto(spec)}
	_, err = cli.Submit(ctx, req)
	return err
}

func (c *WorkerClient) Status(ctx context.Context, node domain.WorkerNode, attemptID string) (dispatcher.WorkerStatus, error) {
	cli, err := c.getClient(node)
	if err != nil {
		return dispatcher.WorkerStatus{}, err
	}
	resp, err := cli.Status(ctx, &pb.StatusRequest{AttemptId: attemptID})
	if err != nil {
		return dispatcher.WorkerStatus{}, err
	}
	return dispatcher.WorkerStatus{
		State:  attemptStateFromProto(resp.Status.State),
		Result: executionResultFromProto(resp.Status.Result),
	}, nil
}

func (c *WorkerClient) Logs(ctx context.Context, node domain.WorkerNode, attemptID string) ([]worker.LogEntry, error) {
	cli, err := c.getClient(node)
	if err != nil {
		return nil, err
	}
	resp, err := cli.Logs(ctx, &pb.LogsRequest{AttemptId: attemptID})
	if err != nil {
		return nil, err
	}
	entries := make([]worker.LogEntry, len(resp.Entries))
	for i, e := range resp.Entries {
		entries[i] = worker.LogEntry{
			Sequence:  e.Sequence,
			Time:      e.Time.AsTime(),
			Stage:     e.Stage,
			Message:   e.Message,
			Code:      int(e.Code),
			Retryable: e.Retryable,
		}
	}
	if entries == nil {
		entries = make([]worker.LogEntry, 0)
	}
	return entries, nil
}

func (c *WorkerClient) Stop(ctx context.Context, node domain.WorkerNode, attemptID string) error {
	cli, err := c.getClient(node)
	if err != nil {
		return err
	}
	_, err = cli.Stop(ctx, &pb.StopRequest{AttemptId: attemptID})
	return err
}

func (c *WorkerClient) Ack(ctx context.Context, node domain.WorkerNode, attemptID string) error {
	cli, err := c.getClient(node)
	if err != nil {
		return err
	}
	_, err = cli.Ack(ctx, &pb.AckRequest{AttemptId: attemptID})
	if err != nil {
		st, _ := status.FromError(err)
		if st.Code() == codes.FailedPrecondition || st.Code() == codes.NotFound {
			return err
		}
		// Ack is best-effort; network errors are not fatal.
		return nil
	}
	return nil
}

func (c *WorkerClient) Health(ctx context.Context, node domain.WorkerNode) (map[string]any, error) {
	cli, err := c.getClient(node)
	if err != nil {
		return nil, err
	}
	resp, err := cli.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"workerId":         resp.WorkerId,
		"version":          resp.Version,
		"pluginVersion":    resp.PluginVersion,
		"algorithmVersion": resp.AlgorithmVersion,
		"captchaPlugin":    resp.CaptchaPlugin,
		"clockCalibration": resp.ClockCalibration,
		"activeAttemptId":  resp.ActiveAttemptId,
	}, nil
}

// =========================================================================
// Proto ↔ domain conversions
// =========================================================================

func specToProto(s domain.ExecutionSpec) *pb.ExecutionSpec {
	p := &pb.ExecutionSpec{
		AttemptId:  s.AttemptID,
		IntentId:   s.IntentID,
		ProjectId:  s.ProjectID,
		ScreenId:   s.ScreenID,
		SkuId:      s.SKUID,
		StartMode:  startModeToProto(s.StartMode),
		IntervalMs: s.IntervalMS,
		Credentials: &pb.Credentials{
			Cookies:       s.Credentials.Cookies,
			RefreshToken:  s.Credentials.RefreshToken,
			Version:       s.Credentials.Version,
			DeviceProfile: s.Credentials.DeviceProfile,
		},
	}
	if !s.StartAt.IsZero() {
		p.StartAt = timestamppb.New(s.StartAt)
	}
	if !s.Deadline.IsZero() {
		p.Deadline = timestamppb.New(s.Deadline)
	}
	for _, b := range s.Buyers {
		p.Buyers = append(p.Buyers, buyerToProto2(b))
	}
	for _, hc := range s.Credentials.CookieJar {
		p.Credentials.CookieJar = append(p.Credentials.CookieJar, &pb.HTTPCookie{
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

func buyerToProto2(b domain.Buyer) *pb.Buyer {
	return &pb.Buyer{
		LogicalId: b.LogicalID,
		BuyerId:   b.BuyerID,
		Name:      b.Name,
		Tel:       b.Tel,
		IdCard:    b.IDCard,
		Type:      int32(b.Type),
	}
}

func startModeToProto(m domain.StartMode) pb.StartMode {
	if m == domain.StartScheduled {
		return pb.StartMode_START_SCHEDULED
	}
	return pb.StartMode_START_IMMEDIATE
}

func attemptStateFromProto(s pb.AttemptState) domain.AttemptState {
	switch s {
	case pb.AttemptState_ATTEMPT_QUEUED:
		return domain.AttemptQueued
	case pb.AttemptState_ATTEMPT_WAITING:
		return domain.AttemptWaiting
	case pb.AttemptState_ATTEMPT_RUNNING:
		return domain.AttemptRunning
	case pb.AttemptState_ATTEMPT_STOPPING:
		return domain.AttemptStopping
	case pb.AttemptState_ATTEMPT_STOPPED:
		return domain.AttemptStopped
	case pb.AttemptState_ATTEMPT_SUCCEEDED:
		return domain.AttemptSucceeded
	case pb.AttemptState_ATTEMPT_FAILED:
		return domain.AttemptFailed
	default:
		return domain.AttemptQueued
	}
}

func failureReasonFromProto(r pb.FailureReason) domain.FailureReason {
	switch r {
	case pb.FailureReason_FAILURE_DEADLINE:
		return domain.FailureDeadline
	case pb.FailureReason_FAILURE_STOPPED:
		return domain.FailureStopped
	case pb.FailureReason_FAILURE_COOKIE_INVALID:
		return domain.FailureCookieInvalid
	case pb.FailureReason_FAILURE_HTTP_412:
		return domain.FailureHTTP412
	case pb.FailureReason_FAILURE_CAPTCHA:
		return domain.FailureCaptcha
	case pb.FailureReason_FAILURE_ACCOUNT_RISK:
		return domain.FailureAccountRisk
	case pb.FailureReason_FAILURE_WORKER_LOST:
		return domain.FailureWorkerLost
	case pb.FailureReason_FAILURE_UNRECOVERABLE:
		return domain.FailureUnrecoverable
	case pb.FailureReason_FAILURE_INTERNAL:
		return domain.FailureInternal
	default:
		return domain.FailureNone
	}
}

func executionResultFromProto(r *pb.ExecutionResult) domain.ExecutionResult {
	if r == nil {
		return domain.ExecutionResult{}
	}
	er := domain.ExecutionResult{
		AttemptID: r.AttemptId,
		IntentID:  r.IntentId,
		SpecHash:  r.SpecHash,
		State:     attemptStateFromProto(r.State),
		Success:   r.Success,
		OrderID:   r.OrderId,
		Reason:    failureReasonFromProto(r.Reason),
		Message:   r.Message,
		Retryable: r.Retryable,
	}
	if r.Credentials != nil {
		er.Credentials = credentialsFromProto(r.Credentials)
	}
	if r.StartedAt != nil {
		er.StartedAt = r.StartedAt.AsTime()
	}
	if r.FinishedAt != nil {
		er.FinishedAt = r.FinishedAt.AsTime()
	}
	return er
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
