//go:build !windows

package plugins

import "syscall"

// Unix 下没有 HideWindow/CreationFlags，给出一个空的或适配的设置
var windowsProcAttr *syscall.SysProcAttr = nil