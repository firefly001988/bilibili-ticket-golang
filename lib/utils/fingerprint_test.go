package utils

import (
	"strings"
	"testing"
)

func TestGenerateRandomFingerprintProducesValidUA(t *testing.T) {
	for range 100 {
		fp := GenerateRandomFingerprint()
		if strings.Contains(fp.UserAgent, "%!") {
			t.Fatalf("malformed user agent: %q", fp.UserAgent)
		}
		if fp.ScreenResolution[0] <= 0 || fp.ScreenResolution[1] <= 0 {
			t.Fatalf("invalid screen resolution: %v", fp.ScreenResolution)
		}
	}
}

func TestGenerateRandomMobileFingerprintKeepsRequestUA(t *testing.T) {
	const ua = "test Android WebView UA"
	fp := GenerateRandomMobileFingerprint(ua)
	if fp.UserAgent != ua {
		t.Fatalf("got UA %q, want %q", fp.UserAgent, ua)
	}
	if fp.Platform != "Linux armv8l" || fp.TouchSupport[0] == 0 {
		t.Fatalf("profile is not mobile-consistent: %+v", fp)
	}
	if len(CalculateFingerprintID(fp)) != 64 {
		t.Fatal("fingerprint ID must be a SHA-256 hex string")
	}
}

func TestMobileFingerprintFromLegacyIsDeterministic(t *testing.T) {
	a := MobileFingerprintFromLegacy("ua", "canvas", "webgl")
	b := MobileFingerprintFromLegacy("ua", "canvas", "webgl")
	if CalculateFingerprintID(a) != CalculateFingerprintID(b) {
		t.Fatal("legacy profile upgrade must be deterministic")
	}
}
