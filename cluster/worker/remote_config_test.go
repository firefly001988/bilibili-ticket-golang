package worker

import (
	"testing"
)

func TestRemoteWorkerConfigRoundTrip(t *testing.T) {
	// Generate a one-time CA + client cert to act as the "employer".
	caCertPEM, caKeyPEM, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	clientCertPEM, clientKeyPEM, err := GenerateClientCert(caCertPEM, caKeyPEM, "test-employer")
	if err != nil {
		t.Fatalf("GenerateClientCert: %v", err)
	}

	// Generate a remote worker config using the employer's CA.
	rc, _, err := GenerateRemoteWorkerConfig(
		caCertPEM, caKeyPEM, clientCertPEM, clientKeyPEM,
		[]string{"test-worker.local", "192.168.1.100"},
		"test-employer",
		RemoteWorkerOptions{
			Listen:          "0.0.0.0:18080",
			DataDir:         "data/test-worker",
			PollIntervalSec: 15,
			WorkerID:        "test-worker-1",
			Version:         "1.0.0",
			CalibrateClock:  true,
		},
	)
	if err != nil {
		t.Fatalf("GenerateRemoteWorkerConfig: %v", err)
	}

	// Encode to Base4096
	encoded, err := rc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if encoded == "" {
		t.Fatal("encoded string is empty")
	}
	t.Logf("Encoded (%d chars): %s", len([]rune(encoded)), encoded)

	// Decode back
	decoded, err := DecodeRemoteWorkerConfig(encoded)
	if err != nil {
		t.Fatalf("DecodeRemoteWorkerConfig: %v", err)
	}

	// Verify round-trip
	if decoded.WorkerID != rc.WorkerID {
		t.Errorf("WorkerID: got %q, want %q", decoded.WorkerID, rc.WorkerID)
	}
	if decoded.Listen != rc.Listen {
		t.Errorf("Listen: got %q, want %q", decoded.Listen, rc.Listen)
	}
	if decoded.CACertPEM != rc.CACertPEM {
		t.Error("CACertPEM mismatch")
	}
	if decoded.ServerCertPEM != rc.ServerCertPEM {
		t.Error("ServerCertPEM mismatch")
	}
	if decoded.ServerKeyPEM != rc.ServerKeyPEM {
		t.Error("ServerKeyPEM mismatch")
	}

	// Verify ToWorkerConfig conversion
	wc := decoded.ToWorkerConfig()
	if string(wc.CACertPEM) != rc.CACertPEM {
		t.Error("ToWorkerConfig CACertPEM mismatch")
	}
	if string(wc.ServerCertPEM) != rc.ServerCertPEM {
		t.Error("ToWorkerConfig ServerCertPEM mismatch")
	}
	if string(wc.ServerKeyPEM) != rc.ServerKeyPEM {
		t.Error("ToWorkerConfig ServerKeyPEM mismatch")
	}
	if wc.WorkerID != rc.WorkerID {
		t.Errorf("WorkerID in Config: got %q, want %q", wc.WorkerID, rc.WorkerID)
	}

	// Verify TLS material can be used to build server config
	serverTLS, err := NewServerTLSConfig(
		[]byte(decoded.CACertPEM),
		[]byte(decoded.ServerCertPEM),
		[]byte(decoded.ServerKeyPEM),
	)
	if err != nil {
		t.Fatalf("NewServerTLSConfig from decoded: %v", err)
	}
	if serverTLS == nil {
		t.Fatal("serverTLS is nil")
	}
}

func TestDecodeInvalidBase4096(t *testing.T) {
	_, err := DecodeRemoteWorkerConfig("not valid base4096 😊")
	if err == nil {
		t.Fatal("expected error for invalid base4096, got nil")
	}
}

func TestDecodeValidBase4096InvalidJSON(t *testing.T) {
	// Base4096 decodes fine, but the resulting bytes are not valid JSON.
	encoded := "啊啊" // encodes 3 zero bytes → [0x00, 0x00, 0x00] → not valid JSON
	_, err := DecodeRemoteWorkerConfig(encoded)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
