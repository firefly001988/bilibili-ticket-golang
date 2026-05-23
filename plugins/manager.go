package plugins

import (
	"bilibili-ticket-golang/plugins/captcha"
	"bilibili-ticket-golang/plugins/pcommon"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
)

// timestampWriter prepends a timestamp to each line written.
type timestampWriter struct {
	w   io.Writer
	buf bytes.Buffer
}

const logFilePath = "logs/plugins.log"
const timeLayout = "2006-01-02 15:04:05 "

func (t *timestampWriter) Write(p []byte) (int, error) {
	written := len(p)
	for _, b := range p {
		if b == '\n' {
			line := append([]byte(time.Now().Format(timeLayout)), t.buf.Bytes()...)
			line = append(line, '\n')
			if _, err := t.w.Write(line); err != nil {
				return 0, err
			}
			t.buf.Reset()
		} else {
			t.buf.WriteByte(b)
		}
	}
	if _, ok := t.w.(*os.File); ok {
		//f.Sync()
	}
	return written, nil
}

// multiple plugin management, e.g. captcha, account info, etc.
type PluginManager struct {
	// Map of plugin name to plugin instance.
	plugins          map[string]*plugin.Client
	mainPluginFolder string
	pluginLogFile    *os.File
	pluginLogWriter  io.Writer
}

func NewPluginManager(mainPluginFolder string) *PluginManager {
	info, err := os.Stat(mainPluginFolder)
	if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
		if info != nil && !info.IsDir() {
			os.Remove(mainPluginFolder)
		}
		err := os.MkdirAll(mainPluginFolder, 0755)
		if err != nil {
			panic(fmt.Sprintf("Failed to create plugin folder '%s': %v", mainPluginFolder, err))
		}
	}

	// Open plugin log file for capturing plugin process stdout/stderr.
	var pluginLogWriter io.Writer = io.Discard
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		pluginLogWriter = &timestampWriter{w: logFile}
	}

	return &PluginManager{
		plugins:          make(map[string]*plugin.Client),
		mainPluginFolder: mainPluginFolder,
		pluginLogFile:    logFile,
		pluginLogWriter:  pluginLogWriter,
	}
}

// RegisterPlugin registers a plugin instance with the manager.
func (pm *PluginManager) LoadPlugin(name string) error {
	var ext string = ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	pluginPath := name + ext
	if _, err := os.Stat(filepath.Join(pm.mainPluginFolder, pluginPath)); os.IsNotExist(err) {
		return fmt.Errorf("plugin not found: %s", name)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("./" + pluginPath)
		cmd.SysProcAttr = windowsProcAttr
	case "linux", "darwin":
		cmd = exec.Command("./" + pluginPath)
	}
	// Set the plugin's working directory to its own folder so it can find
	// relative resources such as ONNX models in ./models/.
	cmd.Dir = pm.mainPluginFolder
	if name == "captcha-plugin" {
		client := plugin.NewClient(&plugin.ClientConfig{
			HandshakeConfig: captcha.Handshake,
			Plugins: map[string]plugin.Plugin{
				"captcha-plugin": &captcha.CaptchaPlugin{},
			},
			Cmd:              cmd,
			AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
			SyncStdout:       io.Discard,
			SyncStderr:       io.Discard,
			Logger: hclog.New(&hclog.LoggerOptions{
				Output:      pm.pluginLogWriter,
				DisableTime: true,
			}),
		})
		pm.plugins[name] = client
		return nil
	} else {
		return fmt.Errorf("unknown plugin name: %s", name)
	}
}

func (pm *PluginManager) UnloadPlugin(name string) {
	if client, exists := pm.plugins[name]; exists {
		client.Kill()
		delete(pm.plugins, name)
	}
}

func (pm *PluginManager) UnloadAll() {
	for name, client := range pm.plugins {
		client.Kill()
		delete(pm.plugins, name)
	}
	if pm.pluginLogFile != nil {
		pm.pluginLogFile.Close()
	}
}

func (pm *PluginManager) IsLoaded(name string) bool {
	_, exists := pm.plugins[name]
	return exists
}

func (pm *PluginManager) GetClient(name string) *plugin.Client {
	client, _ := pm.plugins[name]
	return client
}

// GetVersion retrieves version information from any loaded plugin.
// The plugin must implement the VersionedPlugin interface.
// Returns an error if the plugin is not loaded or doesn't support versioning.
func (pm *PluginManager) GetVersion(name string) (pcommon.VersionInfo, error) {
	client, exists := pm.plugins[name]
	if !exists {
		return pcommon.VersionInfo{}, fmt.Errorf("plugin not loaded: %s", name)
	}

	rpcClient, err := client.Client()
	if err != nil {
		return pcommon.VersionInfo{}, fmt.Errorf("failed to connect to plugin '%s': %v", name, err)
	}

	raw, err := rpcClient.Dispense(name)
	if err != nil {
		return pcommon.VersionInfo{}, fmt.Errorf("failed to dispense plugin '%s': %v", name, err)
	}

	vp, ok := raw.(pcommon.VersionedPlugin)
	if !ok {
		return pcommon.VersionInfo{}, fmt.Errorf("plugin '%s' does not support versioning", name)
	}

	return vp.Version()
}

// GetAllVersions returns version information for all currently loaded plugins.
// Plugins that don't support versioning are skipped silently.
func (pm *PluginManager) GetAllVersions() []pcommon.VersionInfo {
	result := make([]pcommon.VersionInfo, 0)
	for name := range pm.plugins {
		info, err := pm.GetVersion(name)
		if err != nil {
			result = append(result, pcommon.VersionInfo{
				Name:      name,
				Version:   "unknown",
				GitCommit: "unknown",
			})
			continue
		}
		result = append(result, info)
	}
	return result
}
