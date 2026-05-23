// Package pcommon defines types shared between plugins/ and individual plugin
// packages (e.g. plugins/captcha), avoiding circular imports.
package pcommon

// VersionInfo holds the version information for any plugin.
type VersionInfo struct {
	Name      string // e.g. "captcha-plugin"
	GitCommit string // e.g. "a1b2c3d"
	Version   string // e.g. "0.3.2"
}

// VersionedPlugin is an interface that any plugin can implement to expose
// its version information. The PluginManager detects and uses it automatically.
type VersionedPlugin interface {
	Version() (VersionInfo, error)
}
