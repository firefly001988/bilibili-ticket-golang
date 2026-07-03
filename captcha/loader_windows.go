package captcha

import (
	"golang.org/x/sys/windows"
)

func openLibrary(name string) (uintptr, error) {
	dll := windows.NewLazyDLL(name)
	err := dll.Load()
	return dll.Handle(), err
}
