package captcha

import (
	"context"
	"fmt"
	"net/rpc"
	"os/exec"
	"sync"

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
// It mirrors the Click / Slide API from old/captcha/geetest.go.
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

// grpcSolver implements CaptchaSolver by delegating to a CaptchaServiceClient.
// It maintains a map of instance IDs → (gt, challenge) so that the stateless
// gRPC service can be used with the instance-based CaptchaSolver interface.
type grpcSolver struct {
	client    CaptchaServiceClient
	mu        sync.Mutex
	nextID    int
	instances map[string]*instanceState
}

type instanceState struct {
	gt        string
	challenge string
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

// Create stores the gt+challenge pair and returns an instance ID.
func (g *grpcSolver) Create(gt, challenge string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nextID++
	id := fmt.Sprintf("inst-%d", g.nextID)
	g.instances[id] = &instanceState{gt: gt, challenge: challenge}
	return id, nil
}

// Solve runs the full pipeline via the gRPC Solve RPC.
func (g *grpcSolver) Solve(instanceID string) (string, error) {
	st, err := g.getState(instanceID)
	if err != nil {
		return "", err
	}
	resp, err := g.client.Solve(context.Background(), &SolveGeetestCaptchaRequest{
		Gt:        st.gt,
		Challenge: st.challenge,
	})
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("captcha: %s", resp.Error)
	}
	return resp.Validate, nil
}

// GetCS calls the gRPC GetCS RPC.
func (g *grpcSolver) GetCS(instanceID, w string) (*GeetestCS, error) {
	st, err := g.getState(instanceID)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.GetCS(context.Background(), &GetCSRequest{
		Gt:        st.gt,
		Challenge: st.challenge,
		W:         w,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("captcha: %s", resp.Error)
	}
	return resp.Cs, nil
}

// GetType calls the gRPC GetType RPC.
func (g *grpcSolver) GetType(instanceID, w string) (CaptchaType, error) {
	st, err := g.getState(instanceID)
	if err != nil {
		return CaptchaType_UNKNOWN, err
	}
	resp, err := g.client.GetType(context.Background(), &GetTypeRequest{
		Gt:        st.gt,
		Challenge: st.challenge,
		W:         w,
	})
	if err != nil {
		return CaptchaType_UNKNOWN, err
	}
	if !resp.Success {
		return CaptchaType_UNKNOWN, fmt.Errorf("captcha: %s", resp.Error)
	}
	return resp.Type, nil
}

// GetNewCSArgs calls the gRPC GetNewCSArgs RPC.
func (g *grpcSolver) GetNewCSArgs(instanceID string) (*NewCSArgs, error) {
	st, err := g.getState(instanceID)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.GetNewCSArgs(context.Background(), &GetNewCSArgsRequest{
		Gt:        st.gt,
		Challenge: st.challenge,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("captcha: %s", resp.Error)
	}
	return resp.Args, nil
}

// CalculateKey calls the gRPC CalculateKey RPC.
func (g *grpcSolver) CalculateKey(instanceID string, args *NewCSArgs) (string, error) {
	st, err := g.getState(instanceID)
	if err != nil {
		return "", err
	}
	resp, err := g.client.CalculateKey(context.Background(), &CalculateKeyRequest{
		Gt:        st.gt,
		Challenge: st.challenge,
		Args:      args,
	})
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("captcha: %s", resp.Error)
	}
	return resp.Key, nil
}

// GenerateW calls the gRPC GenerateW RPC.
func (g *grpcSolver) GenerateW(instanceID string, key string, args *NewCSArgs) (string, error) {
	st, err := g.getState(instanceID)
	if err != nil {
		return "", err
	}
	resp, err := g.client.GenerateW(context.Background(), &GenerateWRequest{
		Gt:        st.gt,
		Challenge: st.challenge,
		Key:       key,
		Args:      args,
	})
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("captcha: %s", resp.Error)
	}
	return resp.W, nil
}

// Verify calls the gRPC Verify RPC.
func (g *grpcSolver) Verify(instanceID, w string) (string, error) {
	st, err := g.getState(instanceID)
	if err != nil {
		return "", err
	}
	resp, err := g.client.Verify(context.Background(), &VerifyRequest{
		Gt:        st.gt,
		Challenge: st.challenge,
		W:         w,
	})
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("captcha: %s", resp.Error)
	}
	return resp.Validate, nil
}

// Destroy removes the instance state.
func (g *grpcSolver) Destroy(instanceID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.instances, instanceID)
	return nil
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

// GRPCClient creates a CaptchaSolver backed by the gRPC client connection.
func (p *CaptchaPlugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, conn *grpc.ClientConn) (interface{}, error) {
	return &grpcSolver{
		client:    NewCaptchaServiceClient(conn),
		instances: make(map[string]*instanceState),
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
			"captcha": &CaptchaPlugin{},
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
	raw, err := rpcClient.Dispense("captcha")
	if err != nil {
		return nil, err
	}
	return raw.(CaptchaSolver), nil
}
