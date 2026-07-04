package captcha

import (
	"golang.org/x/sys/windows"
)

func openLibrary(name string) (uintptr, error) {
	dll, err := windows.LoadDLL(name)
	if err != nil {
		return 0, err
	}
	return uintptr(dll.Handle), nil
}
