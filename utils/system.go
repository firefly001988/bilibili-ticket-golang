package utils

import "runtime"

func GetSystemArch() string {
	arch := runtime.GOARCH
	return arch
}

func GetSystemOS() string {
	os := runtime.GOOS
	return os
}

func GetSystemInfo() (string, string) {
	return GetSystemOS(), GetSystemArch()
}
