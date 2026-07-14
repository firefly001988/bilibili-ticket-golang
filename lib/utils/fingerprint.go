package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// FingerprintData is a stable, synthetic browser profile. Fields intentionally
// mirror the names collected by common browser fingerprint scripts so the same
// profile can be reused by cookies, signatures and Gaia reports.
type FingerprintData struct {
	UserAgent                 string   `json:"userAgent"`
	ScreenResolution          [2]int   `json:"screenResolution"`
	AvailableScreenResolution [2]int   `json:"availableScreenResolution"`
	ColorDepth                int      `json:"colorDepth"`
	DevicePixelRatio          float64  `json:"devicePixelRatio"`
	Timezone                  string   `json:"timezone"`
	TimezoneOffset            int      `json:"timezoneOffset"`
	Language                  string   `json:"language"`
	Platform                  string   `json:"platform"`
	CPUClass                  string   `json:"cpuClass,omitempty"`
	HardwareConcurrency       int      `json:"hardwareConcurrency"`
	DeviceMemory              int      `json:"deviceMemory"`
	TouchSupport              [3]int   `json:"touchSupport"`
	CookieEnabled             bool     `json:"cookieEnabled"`
	SessionStorage            bool     `json:"sessionStorage"`
	LocalStorage              bool     `json:"localStorage"`
	IndexedDB                 bool     `json:"indexedDb"`
	OpenDatabase              bool     `json:"openDatabase"`
	Webdriver                 bool     `json:"webdriver"`
	HasLiedLanguages          bool     `json:"hasLiedLanguages"`
	HasLiedResolution         bool     `json:"hasLiedResolution"`
	HasLiedOS                 bool     `json:"hasLiedOs"`
	HasLiedBrowser            bool     `json:"hasLiedBrowser"`
	WebGLVendor               string   `json:"webglVendor"`
	WebGLRenderer             string   `json:"webglRenderer"`
	WebGLVendorAndRenderer    string   `json:"webglVendorAndRenderer"`
	WebGLParams               []int    `json:"webglParams"`
	CanvasFingerprint         string   `json:"canvasFingerprint"`
	AudioFingerprint          string   `json:"audioFingerprint"`
	Fonts                     []string `json:"fonts"`
	Plugins                   []string `json:"plugins"`
}

// GenerateRandomFingerprint creates a coherent synthetic desktop profile.
func GenerateRandomFingerprint() FingerprintData {
	ua, platform, renderer := randomDesktopBrowser()
	screen := randomScreenResolution()
	return newFingerprintData(ua, platform, renderer, screen, false)
}

// GenerateRandomMobileFingerprint creates a coherent Android/WebView profile.
// The caller supplies the exact UA used for HTTP requests so the reported UA
// cannot drift from the request headers.
func GenerateRandomMobileFingerprint(userAgent string) FingerprintData {
	renderers := []string{
		"ANGLE (Qualcomm, Adreno (TM) 730, OpenGL ES 3.2)",
		"ANGLE (Qualcomm, Adreno (TM) 740, OpenGL ES 3.2)",
		"ANGLE (ARM, Mali-G78, OpenGL ES 3.2)",
	}
	return newFingerprintData(userAgent, "Linux armv8l", renderers[rng.IntN(len(renderers))], [2]int{1699, 834}, true)
}

// MobileFingerprintFromLegacy upgrades an older four-string device profile
// deterministically. It reuses the saved component values so a restart does
// not silently rotate the synthetic browser identity.
func MobileFingerprintFromLegacy(userAgent, canvas, webgl string) FingerprintData {
	if canvas == "" {
		canvas = "00000000000000000000000000000000"
	}
	if webgl == "" {
		webgl = "ANGLE (Qualcomm, Adreno (TM) 730, OpenGL ES 3.2)"
	}
	fp := newFingerprintData(userAgent, "Linux armv8l", webgl, [2]int{1699, 834}, true)
	fp.HardwareConcurrency = 8
	fp.DeviceMemory = 8
	fp.CanvasFingerprint = canvas
	fp.AudioFingerprint = "124.043475"
	return fp
}

func newFingerprintData(userAgent, platform, renderer string, screen [2]int, mobile bool) FingerprintData {
	availableHeight := screen[1] - 39
	if availableHeight < 1 {
		availableHeight = screen[1]
	}
	touch := [3]int{}
	devicePixelRatio := 1.0
	plugins := []string{"PDF Viewer", "Chrome PDF Viewer", "Chromium PDF Viewer", "Microsoft Edge PDF Viewer", "WebKit built-in PDF"}
	fonts := []string{"Arial", "Arial Black", "Calibri", "Courier New", "Georgia", "Times New Roman", "Verdana"}
	if mobile {
		touch = [3]int{5, 0, 1}
		devicePixelRatio = 3
		plugins = []string{}
		fonts = []string{"Arial", "Droid Sans", "Noto Sans CJK SC", "Roboto", "sans-serif"}
	}

	vendor := "Google Inc. (Qualcomm)"
	if !mobile {
		vendor = "Google Inc. (Intel)"
	}

	return FingerprintData{
		UserAgent:                 userAgent,
		ScreenResolution:          screen,
		AvailableScreenResolution: [2]int{screen[0], availableHeight},
		ColorDepth:                24,
		DevicePixelRatio:          devicePixelRatio,
		Timezone:                  "Asia/Shanghai",
		TimezoneOffset:            -480,
		Language:                  "zh-CN",
		Platform:                  platform,
		HardwareConcurrency:       []int{4, 8, 12}[rng.IntN(3)],
		DeviceMemory:              []int{4, 8}[rng.IntN(2)],
		TouchSupport:              touch,
		CookieEnabled:             true,
		SessionStorage:            true,
		LocalStorage:              true,
		IndexedDB:                 true,
		OpenDatabase:              true,
		Webdriver:                 false,
		WebGLVendor:               vendor,
		WebGLRenderer:             renderer,
		WebGLVendorAndRenderer:    vendor + "~" + renderer,
		WebGLParams:               []int{1, 1, 1, 1, 1, 1, 1, 1},
		CanvasFingerprint:         randomHexString(64),
		AudioFingerprint:          fmt.Sprintf("%d.%06d", 124+rng.IntN(3), rng.IntN(1_000_000)),
		Fonts:                     fonts,
		Plugins:                   plugins,
	}
}

// CalculateFingerprintID generates a deterministic ID from the canonical JSON
// form of a profile. JSON avoids fmt's implementation-specific struct format.
func CalculateFingerprintID(fp FingerprintData) string {
	data, err := json.Marshal(fp)
	if err != nil {
		data = []byte(fmt.Sprintf("%v", fp))
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func randomDesktopBrowser() (userAgent, platform, renderer string) {
	major := 128 + rng.IntN(12)
	build := 6500 + rng.IntN(500)
	patch := 20 + rng.IntN(180)
	switch rng.IntN(3) {
	case 0:
		return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.%d.%d Safari/537.36", major, build, patch),
			"Win32", "ANGLE (Intel, Intel(R) UHD Graphics 620, D3D11)"
	case 1:
		return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:%d.0) Gecko/20100101 Firefox/%d.0", major, major),
			"Win32", "Intel(R) UHD Graphics 620"
	default:
		return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.%d.%d Safari/537.36 Edg/%d.0.%d.%d", major, build, patch, major, build, patch),
			"Win32", "ANGLE (Intel, Intel(R) UHD Graphics 620, D3D11)"
	}
}

func randomScreenResolution() [2]int {
	resolutions := [][2]int{
		{1920, 1080}, {1366, 768}, {1440, 900}, {1536, 864},
		{1600, 900}, {1280, 720}, {2560, 1440},
	}
	return resolutions[rng.IntN(len(resolutions))]
}

func randomHexString(length int) string {
	b := make([]byte, (length+1)/2)
	randBytes(b)
	return hex.EncodeToString(b)[:length]
}
