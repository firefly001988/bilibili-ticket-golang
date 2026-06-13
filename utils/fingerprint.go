package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// FingerprintData represents browser fingerprint data for anti-detection.
type FingerprintData struct {
	UserAgent           string   `json:"userAgent"`
	ScreenResolution    [2]int   `json:"screenResolution"`
	ColorDepth          int      `json:"colorDepth"`
	TimezoneOffset      int      `json:"timezoneOffset"`
	HardwareConcurrency int      `json:"hardwareConcurrency"`
	DeviceMemory        int      `json:"deviceMemory"`
	TouchSupport        [3]bool  `json:"touchSupport"`
	WebGLVendor         string   `json:"webglVendor"`
	WebGLRenderer       string   `json:"webglRenderer"`
	CanvasFingerprint   string   `json:"canvasFingerprint"`
	AudioFingerprint    string   `json:"audioFingerprint"`
	Fonts               []string `json:"fonts"`
	Plugins             []string `json:"plugins"`
}

// GenerateRandomFingerprint creates a random browser fingerprint.
func GenerateRandomFingerprint() FingerprintData {
	return FingerprintData{
		UserAgent:           randomUserAgent(),
		ScreenResolution:    randomScreenResolution(),
		ColorDepth:          randomColorDepth(),
		TimezoneOffset:      randomTimezoneOffset(),
		HardwareConcurrency: randomHardwareConcurrency(),
		DeviceMemory:        randomDeviceMemory(),
		TouchSupport:        randomTouchSupport(),
		WebGLVendor:         randomWebGLVendor(),
		WebGLRenderer:       randomWebGLRenderer(),
		CanvasFingerprint:   randomCanvasFingerprint(),
		AudioFingerprint:    randomAudioFingerprint(),
		Fonts:               randomFonts(),
		Plugins:             randomPlugins(),
	}
}

// CalculateFingerprintID generates a fingerprint ID using SHA256 hash.
func CalculateFingerprintID(fp FingerprintData) string {
	data := fmt.Sprintf("%v", fp)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func randomUserAgent() string {
	browsers := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.%d.%d Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:%d.0) Gecko/20100101 Firefox/%d.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/%d.%d Safari/605.1.15",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.%d.%d Safari/537.36 Edg/%d.0.%d.%d",
	}
	template := browsers[rng.IntN(len(browsers))]
	switch {
	case strings.Contains(template, "Chrome"):
		return fmt.Sprintf(template, 90+rng.IntN(20), rng.IntN(1000), rng.IntN(100), 90+rng.IntN(20), rng.IntN(1000))
	case strings.Contains(template, "Firefox"):
		ver := 90 + rng.IntN(20)
		return fmt.Sprintf(template, ver, ver)
	case strings.Contains(template, "Safari"):
		return fmt.Sprintf(template, 14+rng.IntN(5), rng.IntN(10))
	case strings.Contains(template, "Edg"):
		return fmt.Sprintf(template, 90+rng.IntN(20), rng.IntN(1000), rng.IntN(100), 90+rng.IntN(20), rng.IntN(1000), rng.IntN(100))
	default:
		return browsers[0]
	}
}

func randomScreenResolution() [2]int {
	resolutions := [][2]int{
		{1920, 1080}, {1366, 768}, {1440, 900},
		{1536, 864}, {1600, 900}, {1280, 720},
		{2560, 1440}, {3840, 2160}, {1024, 768},
	}
	return resolutions[rng.IntN(len(resolutions))]
}

func randomColorDepth() int { return 24 }

func randomTimezoneOffset() int { return -720 + rng.IntN(1440) }

func randomHardwareConcurrency() int { return 2 << rng.IntN(4) }

func randomDeviceMemory() int { return 2 << rng.IntN(4) }

func randomTouchSupport() [3]bool {
	return [3]bool{rng.IntN(2) == 1, rng.IntN(2) == 1, rng.IntN(2) == 1}
}

func randomWebGLVendor() string {
	vendors := []string{
		"Google Inc.", "Intel Inc.", "NVIDIA Corporation",
		"AMD", "Apple Inc.", "Microsoft",
	}
	return vendors[rng.IntN(len(vendors))]
}

func randomWebGLRenderer() string {
	renderers := []string{
		"ANGLE (Intel(R) UHD Graphics 620 Direct3D11 vs_5_0 ps_5_0)",
		"ANGLE (NVIDIA GeForce GTX 1060 Direct3D11 vs_5_0 ps_5_0)",
		"ANGLE (AMD Radeon RX 580 Direct3D11 vs_5_0 ps_5_0)",
		"Apple GPU", "Mali-G72", "Adreno (TM) 630",
	}
	return renderers[rng.IntN(len(renderers))]
}

func randomCanvasFingerprint() string {
	return "data:image/png;base64," + randomHexString(32)
}

func randomAudioFingerprint() string {
	return fmt.Sprintf("%d.%d", rng.IntN(1000), rng.IntN(1000000))
}

func randomFonts() []string {
	fonts := []string{"Arial", "Times New Roman", "Courier New", "Georgia", "Verdana", "Helvetica", "Tahoma", "Calibri", "Cambria"}
	count := 3 + rng.IntN(6)
	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = fonts[rng.IntN(len(fonts))]
	}
	return result
}

func randomPlugins() []string {
	plugins := []string{"Chrome PDF Viewer", "Native Client", "Widevine Content Decryption Module", "Microsoft Edge PDF Viewer", "WebKit built-in PDF"}
	count := 1 + rng.IntN(4)
	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = plugins[rng.IntN(len(plugins))]
	}
	return result
}

func randomHexString(length int) string {
	b := make([]byte, length/2)
	randBytes(b)
	return hex.EncodeToString(b)
}
