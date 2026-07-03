package captcha

import (
	"fmt"
	"runtime"
)

func getSystemLibraryPath() string {
	var os, ext string
	switch runtime.GOOS {
	case "darwin":
		os = "darwin"
		ext = ".dylib"
	case "linux":
		os = "linux"
		ext = ".so"
	case "windows":
		os = "windows"
		ext = ".dll"
	default:
		panic(fmt.Errorf("GOOS=%s is not supported", runtime.GOOS))
	}
	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch = "arm64"
	default:
		panic(fmt.Errorf("GOARCH=%s is not supported", runtime.GOARCH))
	}
	return fmt.Sprintf("libcaptcha-%s-%s%s", os, arch, ext)
}
