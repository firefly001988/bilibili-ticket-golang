package worker

import (
	"encoding/json"

	"bilibili-ticket-golang/lib/utils/base4096"
)

// RemoteWorkerConfig is a self-contained, distributable worker configuration
// that includes TLS PEM material alongside all worker.Config fields.
// It is serialised to JSON and then encoded with Base4096 for safe
// copy-paste distribution.
type RemoteWorkerConfig struct {
	Listen           string `json:"listen,omitempty"`
	DataDir          string `json:"dataDir,omitempty"`
	PollIntervalSec  int    `json:"pollIntervalSec,omitempty"`
	LeaseDurationSec int    `json:"leaseDurationSec,omitempty"`
	WorkerID         string `json:"workerId,omitempty"`
	Version          string `json:"version,omitempty"`
	PluginVersion    string `json:"pluginVersion,omitempty"`
	AlgorithmVersion string `json:"algorithmVersion,omitempty"`
	PluginDir        string `json:"pluginDir,omitempty"`
	CaptchaPlugin    string `json:"captchaPlugin,omitempty"`
	CalibrateClock   bool   `json:"calibrateClock,omitempty"`

	// TLS material — these are the PEM-encoded certificates and keys
	// needed by both the worker (server side) and the employer (client side)
	// to establish mTLS.
	//
	// Worker side:
	CACertPEM     string `json:"caCertPEM"`
	ServerCertPEM string `json:"serverCertPEM"`
	ServerKeyPEM  string `json:"serverKeyPEM"`

	// Employer side — allows the employer to connect without having
	// previously generated or stored the client certificate.
	EmployerCertPEM string `json:"employerCertPEM,omitempty"`
	EmployerKeyPEM  string `json:"employerKeyPEM,omitempty"`
}

// ToWorkerConfig converts the remote configuration to a standard worker.Config.
// The TLS PEM fields are set directly so that Normalize() will not attempt
// to auto-generate or load from disk.
func (rc *RemoteWorkerConfig) ToWorkerConfig() Config {
	return Config{
		Listen:           rc.Listen,
		DataDir:          rc.DataDir,
		PollIntervalSec:  rc.PollIntervalSec,
		LeaseDurationSec: rc.LeaseDurationSec,
		WorkerID:         rc.WorkerID,
		Version:          rc.Version,
		PluginVersion:    rc.PluginVersion,
		AlgorithmVersion: rc.AlgorithmVersion,
		PluginDir:        rc.PluginDir,
		CaptchaPlugin:    rc.CaptchaPlugin,
		CalibrateClock:   rc.CalibrateClock,
		CACertPEM:        []byte(rc.CACertPEM),
		ServerCertPEM:    []byte(rc.ServerCertPEM),
		ServerKeyPEM:     []byte(rc.ServerKeyPEM),
	}
}

// Encode serialises the configuration to JSON and returns its Base4096
// representation.  The resulting string is safe to copy-paste and share.
func (rc *RemoteWorkerConfig) Encode() (string, error) {
	raw, err := json.Marshal(rc)
	if err != nil {
		return "", err
	}
	return base4096.Encode(raw), nil
}

// DecodeRemoteWorkerConfig decodes a Base4096-encoded JSON string back
// into a RemoteWorkerConfig.
func DecodeRemoteWorkerConfig(encoded string) (*RemoteWorkerConfig, error) {
	raw, err := base4096.Decode(encoded)
	if err != nil {
		return nil, err
	}
	var rc RemoteWorkerConfig
	if err := json.Unmarshal(raw, &rc); err != nil {
		return nil, err
	}
	return &rc, nil
}
