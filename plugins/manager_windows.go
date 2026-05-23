//go:build windows

package plugin

import "syscall"

var windowsProcAttr = &syscall.SysProcAttr{
	HideWindow:    true,
	CreationFlags: 0x08000000,
}
