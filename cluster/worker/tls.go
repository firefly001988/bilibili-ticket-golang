package worker

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// TLSBundle holds a complete set of PEM-encoded TLS material for one side.
type TLSBundle struct {
	CAPEM      []byte // CA certificate
	CertPEM    []byte // leaf certificate
	KeyPEM     []byte // leaf private key
	ServerName string // SNI hostname in the certificate
}

// GenerateCA creates a self‑signed CA certificate and key.
func GenerateCA() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "BilibiliTicket Worker CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create CA cert: %w", err)
	}
	return pemEncodeCert(certDER), pemEncodeECKey(key), nil
}

// GenerateServerCert creates a server certificate signed by the CA for the
// given hosts (DNS names and/or IP addresses). The common name is set to the
// first host.
func GenerateServerCert(caCertPEM, caKeyPEM []byte, hosts []string) (certPEM, keyPEM []byte, err error) {
	ca, err := parseCA(caCertPEM, caKeyPEM)
	if err != nil {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate server key: %w", err)
	}
	cn := "worker"
	if len(hosts) > 0 {
		cn = hosts[0]
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, nil, fmt.Errorf("create server cert: %w", err)
	}
	return pemEncodeCert(certDER), pemEncodeECKey(key), nil
}

// GenerateClientCert creates a client certificate signed by the CA.
func GenerateClientCert(caCertPEM, caKeyPEM []byte, commonName string) (certPEM, keyPEM []byte, err error) {
	ca, err := parseCA(caCertPEM, caKeyPEM)
	if err != nil {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate client key: %w", err)
	}
	if commonName == "" {
		commonName = "client"
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, nil, fmt.Errorf("create client cert: %w", err)
	}
	return pemEncodeCert(certDER), pemEncodeECKey(key), nil
}

// NewServerTLSConfig builds a *tls.Config that requires a client certificate
// signed by the given CA.
func NewServerTLSConfig(caPEM, certPEM, keyPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse server key pair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("append CA cert to pool")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// NewClientTLSConfig builds a *tls.Config for an mTLS client.
func NewClientTLSConfig(caPEM, certPEM, keyPEM []byte, serverName string) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse client key pair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("append CA cert to pool")
	}
	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            pool,
		ServerName:         serverName,
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: false,
	}, nil
}

// LoadOrGenerateLocalTLS loads TLS material from disk or generates a fresh
// local CA + client cert.  Returns the bundle and true if freshly generated.
func LoadOrGenerateLocalTLS(dir string) (*TLSBundle, bool, error) {
	caCertPath := filepath.Join(dir, "ca.pem")
	caKeyPath := filepath.Join(dir, "ca-key.pem")
	clientCertPath := filepath.Join(dir, "client.pem")
	clientKeyPath := filepath.Join(dir, "client-key.pem")
	serverCertPath := filepath.Join(dir, "server.pem")
	serverKeyPath := filepath.Join(dir, "server-key.pem")

	if fileExists(caCertPath) && fileExists(caKeyPath) &&
		fileExists(clientCertPath) && fileExists(clientKeyPath) &&
		fileExists(serverCertPath) && fileExists(serverKeyPath) {
		caPEM, _ := os.ReadFile(caCertPath)
		_, _ = os.ReadFile(caKeyPath) // only checked for existence
		clientCertPEM, _ := os.ReadFile(clientCertPath)
		clientKeyPEM, _ := os.ReadFile(clientKeyPath)
		return &TLSBundle{
			CAPEM:   caPEM,
			CertPEM: clientCertPEM,
			KeyPEM:  clientKeyPEM,
		}, false, nil
	}

	// Generate fresh
	caCertPEM, caKeyPEM, err := GenerateCA()
	if err != nil {
		return nil, false, err
	}
	serverCertPEM, serverKeyPEM, err := GenerateServerCert(caCertPEM, caKeyPEM, []string{"localhost", "127.0.0.1"})
	if err != nil {
		return nil, false, err
	}
	clientCertPEM, clientKeyPEM, err := GenerateClientCert(caCertPEM, caKeyPEM, "local-client")
	if err != nil {
		return nil, false, err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, false, err
	}
	for _, f := range [][2]string{
		{caCertPath, string(caCertPEM)},
		{caKeyPath, string(caKeyPEM)},
		{clientCertPath, string(clientCertPEM)},
		{clientKeyPath, string(clientKeyPEM)},
		{serverCertPath, string(serverCertPEM)},
		{serverKeyPath, string(serverKeyPEM)},
	} {
		if err := os.WriteFile(f[0], []byte(f[1]), 0600); err != nil {
			return nil, false, err
		}
	}

	return &TLSBundle{
		CAPEM:   caCertPEM,
		CertPEM: clientCertPEM,
		KeyPEM:  clientKeyPEM,
	}, true, nil
}

// LoadLocalServerTLS loads the server-side TLS material.
func LoadLocalServerTLS(dir string) (caPEM, certPEM, keyPEM []byte, err error) {
	caPEM, err = os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read ca.pem: %w", err)
	}
	certPEM, err = os.ReadFile(filepath.Join(dir, "server.pem"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read server.pem: %w", err)
	}
	keyPEM, err = os.ReadFile(filepath.Join(dir, "server-key.pem"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read server-key.pem: %w", err)
	}
	return caPEM, certPEM, keyPEM, nil
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

type caPair struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func parseCA(caCertPEM, caKeyPEM []byte) (*caPair, error) {
	certBlock, _ := pem.Decode(caCertPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("decode CA cert PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}
	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("decode CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA key: %w", err)
	}
	return &caPair{cert: cert, key: key}, nil
}

func pemEncodeCert(der []byte) []byte {
	var buf bytes.Buffer
	_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	return buf.Bytes()
}

func pemEncodeECKey(key *ecdsa.PrivateKey) []byte {
	der, _ := x509.MarshalECPrivateKey(key)
	var buf bytes.Buffer
	_ = pem.Encode(&buf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return buf.Bytes()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// RemoteWorkerOptions holds the non-TLS fields for a worker configuration
// that is being generated for remote distribution.
type RemoteWorkerOptions struct {
	Listen           string
	DataDir          string
	PollIntervalSec  int
	LeaseDurationSec int
	WorkerID         string
	Version          string
	PluginDir        string
	CaptchaPlugin    string
	CalibrateClock   bool
}

// GenerateRemoteWorkerConfig creates a complete TLS setup for a remote worker
// and the corresponding employer-side credentials.
//
// It generates:
//   - A self-signed CA
//   - A server certificate for the worker (signed by the CA, valid for the
//     given hosts)
//   - A client certificate for the employer (signed by the same CA)
//
// Returns:
//   - RemoteWorkerConfig — the full worker configuration including PEM, ready
//     to be Base4096-encoded and distributed to the worker
//   - TLSBundle — the employer-side credentials (CA + client cert + key) that
//     the employer must keep to establish mTLS connections
func GenerateRemoteWorkerConfig(hosts []string, clientCommonName string, opts RemoteWorkerOptions) (*RemoteWorkerConfig, *TLSBundle, error) {
	// Generate CA
	caCertPEM, caKeyPEM, err := GenerateCA()
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA: %w", err)
	}

	// Generate server certificate for the worker
	serverCertPEM, serverKeyPEM, err := GenerateServerCert(caCertPEM, caKeyPEM, hosts)
	if err != nil {
		return nil, nil, fmt.Errorf("generate server cert: %w", err)
	}

	// Generate client certificate for the employer
	cn := clientCommonName
	if cn == "" {
		cn = "employer-client"
	}
	clientCertPEM, clientKeyPEM, err := GenerateClientCert(caCertPEM, caKeyPEM, cn)
	if err != nil {
		return nil, nil, fmt.Errorf("generate client cert: %w", err)
	}

	serverName := "worker"
	if len(hosts) > 0 {
		serverName = hosts[0]
	}

	rc := &RemoteWorkerConfig{
		Listen:           opts.Listen,
		DataDir:          opts.DataDir,
		PollIntervalSec:  opts.PollIntervalSec,
		LeaseDurationSec: opts.LeaseDurationSec,
		WorkerID:         opts.WorkerID,
		Version:          opts.Version,
		PluginDir:        opts.PluginDir,
		CaptchaPlugin:    opts.CaptchaPlugin,
		CalibrateClock:   opts.CalibrateClock,
		CACertPEM:        string(caCertPEM),
		ServerCertPEM:    string(serverCertPEM),
		ServerKeyPEM:     string(serverKeyPEM),
	}

	bundle := &TLSBundle{
		CAPEM:      caCertPEM,
		CertPEM:    clientCertPEM,
		KeyPEM:     clientKeyPEM,
		ServerName: serverName,
	}

	return rc, bundle, nil
}
