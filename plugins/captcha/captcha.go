package captcha

import (
	"context"
	"fmt"
	"net/rpc"
	"os/exec"
	"sync"

	"bilibili-ticket-golang/plugins/pcommon"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

// =============================================================================
// Handshake – must match between host and plugin
// =============================================================================

var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "BILI_CAPTCHA_PLUGIN",
	MagicCookieValue: "g3t3st-s0lv3r",
}

// =============================================================================
// CaptchaSolver – the shared interface (plugin contract)
// =============================================================================

// CaptchaSolver is the interface that the captcha plugin must implement.
type CaptchaSolver interface {
	// Create initialises a solver instance for the given gt+challenge.
	// Returns the instance ID for subsequent calls.
	Create(gt, challenge string) (instanceID string, err error)

	// Solve runs the full pipeline and returns the validate string.
	Solve(instanceID string) (validate string, err error)

	// GetCS returns the initial C/S data.
	GetCS(instanceID, w string) (*GeetestCS, error)

	// GetType returns the captcha type (Click or Slide).
	GetType(instanceID, w string) (CaptchaType, error)

	// GetNewCSArgs fetches new challenge args including image URLs.
	GetNewCSArgs(instanceID string) (*NewCSArgs, error)

	// CalculateKey computes the key from NewCSArgs.
	CalculateKey(instanceID string, args *NewCSArgs) (key string, err error)

	// GenerateW generates the w parameter.
	GenerateW(instanceID string, key string, args *NewCSArgs) (w string, err error)

	// Verify submits the w parameter and returns the validate string.
	Verify(instanceID, w string) (validate string, err error)

	// Destroy releases the solver instance.
	Destroy(instanceID string) error
}

// =============================================================================
// gRPC client wrapper – bridges CaptchaSolver over the gRPC proto client
// =============================================================================

// grpcSolver implements CaptchaSolver by delegating to ClickServiceClient
// and SlideServiceClient. It maintains a map of instance IDs → state so
// that the stateless gRPC services can be used with the instance-based
// CaptchaSolver interface.
type grpcSolver struct {
	clickClient ClickServiceClient
	slideClient SlideServiceClient
	mu          sync.Mutex
	nextID      int
	instances   map[string]*instanceState
}

type instanceState struct {
	mu          sync.Mutex
	gt          string
	challenge   string
	captchaType CaptchaType // set after GetType; defaults to UNKNOWN
}

func (g *grpcSolver) getState(id string) (*instanceState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st, ok := g.instances[id]
	if !ok {
		return nil, fmt.Errorf("captcha: unknown instance %q", id)
	}
	return st, nil
}

func (g *grpcSolver) getTypeLocked(id string) (CaptchaType, error) {
	st, err := g.getState(id)
	if err != nil {
		return CaptchaType_UNKNOWN, err
	}
	st.mu.Lock()
	t := st.captchaType
	st.mu.Unlock()
	if t != CaptchaType_UNKNOWN {
		return t, nil
	}
	return g.GetType(id, "")
}

// Create stores the gt+challenge pair and returns an instance ID.
func (g *grpcSolver) Create(gt, challenge string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	id := fmt.Sprintf("inst-%d", g.nextID)
	g.instances[id] = &instanceState{
		gt:          gt,
		challenge:   challenge,
		captchaType: CaptchaType_UNKNOWN,
	}
	return id, nil
}

// Solve runs the full pipeline via the appropriate gRPC Solve RPC.
// If the captcha type is not yet known, GetType is called first to
// auto-detect whether to use ClickService or SlideService.
func (g *grpcSolver) Solve(instanceID string) (string, error) {
	t, err := g.getTypeLocked(instanceID)
	if err != nil {
		return "", err
	}
	st, err := g.getState(instanceID)
	if err != nil {
		return "", err
	}
	req := &SolveGeetestCaptchaRequest{Gt: st.gt, Challenge: st.challenge}
	switch t {
	case CaptchaType_SLIDE:
		resp, err := g.slideClient.Solve(context.Background(), req)
		if err != nil {
			return "", err
		}
		if !resp.Success {
			return "", fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Validate, nil
	default: // CLICK
		resp, err := g.clickClient.Solve(context.Background(), req)
		if err != nil {
			return "", err
		}
		if !resp.Success {
			return "", fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Validate, nil
	}
}

// GetCS calls the gRPC GetCS RPC. If the captcha type is not yet known,
// it is auto-detected first so that the request is dispatched to the
// correct service (ClickService vs SlideService).
func (g *grpcSolver) GetCS(instanceID, w string) (*GeetestCS, error) {
	t, err := g.getTypeLocked(instanceID)
	if err != nil {
		return nil, err
	}
	st, err := g.getState(instanceID)
	if err != nil {
		return nil, err
	}
	req := &GetCSRequest{Gt: st.gt, Challenge: st.challenge, W: w}
	switch t {
	case CaptchaType_SLIDE:
		resp, err := g.slideClient.GetCS(context.Background(), req)
		if err != nil {
			return nil, err
		}
		if !resp.Success {
			return nil, fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Cs, nil
	default:
		resp, err := g.clickClient.GetCS(context.Background(), req)
		if err != nil {
			return nil, err
		}
		if !resp.Success {
			return nil, fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Cs, nil
	}
}

// GetType calls the gRPC GetType RPC and caches the result on the instance.
func (g *grpcSolver) GetType(instanceID, w string) (CaptchaType, error) {
	st, err := g.getState(instanceID)
	if err != nil {
		return CaptchaType_UNKNOWN, err
	}
	req := &GetTypeRequest{Gt: st.gt, Challenge: st.challenge, W: w}
	resp, err := g.clickClient.GetType(context.Background(), req)
	if err != nil {
		return CaptchaType_UNKNOWN, err
	}
	if !resp.Success {
		return CaptchaType_UNKNOWN, fmt.Errorf("captcha: %s", resp.Error)
	}
	st.mu.Lock()
	st.captchaType = resp.Type
	st.mu.Unlock()
	return resp.Type, nil
}

// GetNewCSArgs calls the gRPC GetNewCSArgs RPC.
func (g *grpcSolver) GetNewCSArgs(instanceID string) (*NewCSArgs, error) {
	t, err := g.getTypeLocked(instanceID)
	if err != nil {
		return nil, err
	}
	st, err := g.getState(instanceID)
	if err != nil {
		return nil, err
	}
	req := &GetNewCSArgsRequest{Gt: st.gt, Challenge: st.challenge}
	switch t {
	case CaptchaType_SLIDE:
		resp, err := g.slideClient.GetNewCSArgs(context.Background(), req)
		if err != nil {
			return nil, err
		}
		if !resp.Success {
			return nil, fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Args, nil
	default:
		resp, err := g.clickClient.GetNewCSArgs(context.Background(), req)
		if err != nil {
			return nil, err
		}
		if !resp.Success {
			return nil, fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Args, nil
	}
}

// CalculateKey calls the gRPC CalculateKey RPC.
func (g *grpcSolver) CalculateKey(instanceID string, args *NewCSArgs) (string, error) {
	t, err := g.getTypeLocked(instanceID)
	if err != nil {
		return "", err
	}
	st, err := g.getState(instanceID)
	if err != nil {
		return "", err
	}
	req := &CalculateKeyRequest{Gt: st.gt, Challenge: st.challenge, Args: args}
	switch t {
	case CaptchaType_SLIDE:
		resp, err := g.slideClient.CalculateKey(context.Background(), req)
		if err != nil {
			return "", err
		}
		if !resp.Success {
			return "", fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Key, nil
	default:
		resp, err := g.clickClient.CalculateKey(context.Background(), req)
		if err != nil {
			return "", err
		}
		if !resp.Success {
			return "", fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Key, nil
	}
}

// GenerateW calls the gRPC GenerateW RPC.
func (g *grpcSolver) GenerateW(instanceID string, key string, args *NewCSArgs) (string, error) {
	t, err := g.getTypeLocked(instanceID)
	if err != nil {
		return "", err
	}
	st, err := g.getState(instanceID)
	if err != nil {
		return "", err
	}
	req := &GenerateWRequest{Gt: st.gt, Challenge: st.challenge, Key: key, Args: args}
	switch t {
	case CaptchaType_SLIDE:
		resp, err := g.slideClient.GenerateW(context.Background(), req)
		if err != nil {
			return "", err
		}
		if !resp.Success {
			return "", fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.W, nil
	default:
		resp, err := g.clickClient.GenerateW(context.Background(), req)
		if err != nil {
			return "", err
		}
		if !resp.Success {
			return "", fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.W, nil
	}
}

// Verify calls the gRPC Verify RPC.
func (g *grpcSolver) Verify(instanceID, w string) (string, error) {
	t, err := g.getTypeLocked(instanceID)
	if err != nil {
		return "", err
	}
	st, err := g.getState(instanceID)
	if err != nil {
		return "", err
	}
	req := &VerifyRequest{Gt: st.gt, Challenge: st.challenge, W: w}
	switch t {
	case CaptchaType_SLIDE:
		resp, err := g.slideClient.Verify(context.Background(), req)
		if err != nil {
			return "", err
		}
		if !resp.Success {
			return "", fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Validate, nil
	default:
		resp, err := g.clickClient.Verify(context.Background(), req)
		if err != nil {
			return "", err
		}
		if !resp.Success {
			return "", fmt.Errorf("captcha: %s", resp.Error)
		}
		return resp.Validate, nil
	}
}

// Destroy removes the instance state.
func (g *grpcSolver) Destroy(instanceID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.instances[instanceID]; !ok {
		return fmt.Errorf("captcha: unknown instance %q", instanceID)
	}
	delete(g.instances, instanceID)
	return nil
}

// Version returns the plugin version information (uses ClickService).
func (g *grpcSolver) Version() (pcommon.VersionInfo, error) {
	resp, err := g.clickClient.Version(context.Background(), &VersionRequest{})
	if err != nil {
		return pcommon.VersionInfo{}, err
	}
	return pcommon.VersionInfo{
		Name:      "captcha-plugin",
		GitCommit: resp.GitCommit,
		Version:   resp.Version,
	}, nil
}

// =============================================================================
// go-plugin GRPCPlugin integration
// =============================================================================

// CaptchaPlugin implements both plugin.Plugin (for the plugin map entry)
// and plugin.GRPCPlugin (for the actual gRPC communication).
// The Rust binary serves gRPC; the Go host consumes it via GRPCClient.
type CaptchaPlugin struct{}

// ---- plugin.Plugin (net/rpc fallback, unused) ----

func (p *CaptchaPlugin) Server(broker *plugin.MuxBroker) (interface{}, error) {
	return nil, fmt.Errorf("captcha: gRPC-only plugin, net/rpc Server not supported")
}

func (p *CaptchaPlugin) Client(broker *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return nil, fmt.Errorf("captcha: gRPC-only plugin, net/rpc Client not supported")
}

// ---- plugin.GRPCPlugin ----

// GRPCServer is not used – the Rust binary serves gRPC.
func (p *CaptchaPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	return nil
}

// GRPCClient creates a CaptchaSolver backed by both ClickService and SlideService clients.
func (p *CaptchaPlugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, conn *grpc.ClientConn) (interface{}, error) {
	return &grpcSolver{
		clickClient: NewClickServiceClient(conn),
		slideClient: NewSlideServiceClient(conn),
		instances:   make(map[string]*instanceState),
	}, nil
}

// =============================================================================
// Client helpers
// =============================================================================

// NewClient creates a go-plugin client that launches the captcha plugin binary
// and connects via gRPC over stdin/stdout.
//
// Usage:
//
//	client := captcha.NewClient("./plugins/captcha/captcha-plugin")
//	defer client.Kill()
//	solver, _ := captcha.Dispense(client)
//	id, _ := solver.Create("gt_value", "challenge_value")
//	validate, _ := solver.Solve(id)
func NewClient(pluginPath string) *plugin.Client {
	return plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: Handshake,
		Plugins: map[string]plugin.Plugin{
			"captcha-plugin": &CaptchaPlugin{},
		},
		Cmd:              exec.Command(pluginPath),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
	})
}

// Dispense obtains the CaptchaSolver from a running plugin.Client.
func Dispense(client *plugin.Client) (CaptchaSolver, error) {
	rpcClient, err := client.Client()
	if err != nil {
		return nil, err
	}
	raw, err := rpcClient.Dispense("captcha-plugin")
	if err != nil {
		return nil, err
	}
	return raw.(CaptchaSolver), nil
}
