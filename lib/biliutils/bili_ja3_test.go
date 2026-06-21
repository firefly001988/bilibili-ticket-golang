package biliutils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/bertold/req/v3"
)

// TLSBrowserLeaksResp mirrors the JSON returned by https://tls.browserleaks.com/json
type TLSBrowserLeaksResp struct {
	UserAgent  string `json:"user_agent"`
	JA3Hash    string `json:"ja3_hash"`
	JA3Text    string `json:"ja3_text"`
	JA3NHash   string `json:"ja3n_hash"`
	JA3NText   string `json:"ja3n_text"`
	JA4        string `json:"ja4"`
	JA4R       string `json:"ja4_r"`
	JA4RO      string `json:"ja4_ro"`
	JA4O       string `json:"ja4_o"`
	AkamaiHash string `json:"akamai_hash"`
	AkamaiText string `json:"akamai_text"`
}

// fetchJA3 creates a req.Client with the given TLS fingerprint config,
// calls browserleaks, and returns the parsed TLS fingerprint response.
func fetchJA3(client *req.Client) (*TLSBrowserLeaksResp, error) {
	resp, err := client.R().
		SetHeader("Accept", "application/json").
		Get("https://tls.browserleaks.com/json")
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(resp.Bytes()))
	}

	var r TLSBrowserLeaksResp
	if err := json.Unmarshal(resp.Bytes(), &r); err != nil {
		return nil, fmt.Errorf("unmarshal failed: %w\nbody: %s", err, string(resp.Bytes()))
	}
	return &r, nil
}

// printJA3 pretty-prints the JA3 fingerprint info.
func printJA3(label string, r *TLSBrowserLeaksResp) {
	fmt.Printf("=== %s ===\n", label)
	fmt.Printf("  User-Agent : %s\n", r.UserAgent)
	fmt.Printf("  JA3 Hash   : %s\n", r.JA3Hash)
	fmt.Printf("  JA3 Text   : %s\n", r.JA3Text)
	fmt.Printf("  JA3N Hash  : %s\n", r.JA3NHash)
	fmt.Printf("  JA3N Text  : %s\n", r.JA3NText)
	fmt.Printf("  JA4        : %s\n", r.JA4)
	fmt.Printf("  Akamai     : %s\n", r.AkamaiHash)
	fmt.Println()
}

// TestJA3Fingerprint hits browserleaks with multiple TLS fingerprint profiles
// and prints the resulting JA3 hashes.  Run with: go test -run TestJA3Fingerprint -v ./biliutils/
func TestJA3Fingerprint(t *testing.T) {
	testCases := []struct {
		label string
		setup func(*req.Client) *req.Client
	}{
		{
			label: "Android + Chrome impersonation (current BiliClient default)",
			setup: func(c *req.Client) *req.Client {
				return c.SetTLSFingerprintAndroid().ImpersonateChrome()
			},
		},
		{
			label: "Chrome TLS fingerprint + Chrome impersonation",
			setup: func(c *req.Client) *req.Client {
				return c.SetTLSFingerprintChrome().ImpersonateChrome()
			},
		},
		{
			label: "Firefox TLS fingerprint + Firefox impersonation",
			setup: func(c *req.Client) *req.Client {
				return c.SetTLSFingerprintFirefox().ImpersonateFirefox()
			},
		},
		{
			label: "Safari TLS fingerprint + Safari impersonation",
			setup: func(c *req.Client) *req.Client {
				return c.SetTLSFingerprintSafari().ImpersonateSafari()
			},
		},
		{
			label: "Edge TLS fingerprint + Chrome impersonation",
			setup: func(c *req.Client) *req.Client {
				return c.SetTLSFingerprintEdge().ImpersonateChrome()
			},
		},
		{
			label: "Randomized TLS fingerprint + Chrome impersonation",
			setup: func(c *req.Client) *req.Client {
				return c.SetTLSFingerprintRandomized().ImpersonateChrome()
			},
		},
		{
			label: "No TLS fingerprint (Go default crypto/tls)",
			setup: func(c *req.Client) *req.Client {
				return c
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.label, func(t *testing.T) {
			client := tc.setup(req.C())

			r, err := fetchJA3(client)
			if err != nil {
				t.Fatalf("fetchJA3 failed: %v", err)
			}
			printJA3(tc.label, r)

			if r.JA3Hash == "" {
				t.Error("JA3Hash is empty — browserleaks may not have returned TLS info")
			}
		})

		// Small delay between tests to avoid rate-limiting
		time.Sleep(500 * time.Millisecond)
	}
}

// TestBiliClientJA3 creates a real BiliClient and checks its JA3 fingerprint.
// Run with: go test -run TestBiliClientJA3 -v ./biliutils/
func TestBiliClientJA3(t *testing.T) {
	bc, err := NewBiliClient()
	if err != nil {
		t.Fatalf("NewBiliClient failed: %v", err)
	}

	// The BiliClient's internal req.Client already has
	// SetTLSFingerprintAndroid().ImpersonateChrome() applied.
	r, err := fetchJA3(bc.client)
	if err != nil {
		t.Fatalf("fetchJA3 failed: %v", err)
	}
	printJA3("BiliClient (internal req.Client)", r)

	t.Logf("BiliClient JA3: %s", r.JA3Hash)
	t.Logf("BiliClient JA4: %s", r.JA4)
}
