package plugins

import (
	"bilibili-ticket-golang/lib/plugins/pcommon"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/hashicorp/go-plugin"
)

// timestampWriter prepends a timestamp to each line written.
type timestampWriter struct {
	w   io.Writer
	mu  sync.Mutex
	buf bytes.Buffer
}

const logFilePath = "logs/plugins.log"
const timeLayout = "2006-01-02 15:04:05 "

func (t *timestampWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	consumed := 0
	for _, b := range p {
		consumed++
		if b == '\n' {
			line := append([]byte(time.Now().Format(timeLayout)), t.buf.Bytes()...)
			line = append(line, '\n')
			if _, err := t.w.Write(line); err != nil {
				return consumed, err
			}
			t.buf.Reset()
		} else {
			t.buf.WriteByte(b)
		}
	}
	return len(p), nil
}

// multiple plugin management, e.g. captcha, account info, etc.
type PluginManager struct {
	// Map of plugin name to plugin instance.
	plugins          map[string]*plugin.Client
	testResults      map[string]string
	mainPluginFolder string
	pluginLogFile    *os.File
	pluginLogWriter  io.Writer
	mu               sync.RWMutex
}

// SetTestResult saves a test result string for the plugin.
func (pm *PluginManager) SetTestResult(name, result string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.testResults[name] = result
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
		testResults:      make(map[string]string),
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
	pluginInfo, statErr := os.Stat(filepath.Join(pm.mainPluginFolder, pluginPath))
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return fmt.Errorf("plugin not found: %s", name)
		}
		return fmt.Errorf("plugin stat failed: %w", statErr)
	}
	if !pluginInfo.Mode().IsRegular() {
		return fmt.Errorf("plugin path is not a regular file: %s", name)
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

	return fmt.Errorf("unknown plugin name: %s", name)
}

func (pm *PluginManager) UnloadPlugin(name string) {
	pm.mu.Lock()
	client, exists := pm.plugins[name]
	if exists {
		delete(pm.plugins, name)
	}
	pm.mu.Unlock()
	if exists {
		client.Kill()
	}
}

func (pm *PluginManager) UnloadAll() {
	pm.mu.Lock()
	clients := make([]*plugin.Client, 0, len(pm.plugins))
	for name, client := range pm.plugins {
		clients = append(clients, client)
		delete(pm.plugins, name)
	}
	pm.mu.Unlock()
	for _, client := range clients {
		client.Kill()
	}
	if pm.pluginLogFile != nil {
		pm.pluginLogFile.Close()
	}
}

func (pm *PluginManager) IsLoaded(name string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	_, exists := pm.plugins[name]
	return exists
}

func (pm *PluginManager) GetClient(name string) *plugin.Client {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.plugins[name]
}

// GetVersion retrieves version information from any loaded plugin.
// The plugin must implement the VersionedPlugin interface.
// Returns an error if the plugin is not loaded or doesn't support versioning.
func (pm *PluginManager) GetVersion(name string) (pcommon.VersionInfo, error) {
	pm.mu.RLock()
	client, exists := pm.plugins[name]
	pm.mu.RUnlock()
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

type LoadedPluginInfo struct {
	Name       string
	GitCommit  string
	Version    string
	TestResult string
}

// GetAllVersions returns version information for all currently loaded plugins.
// Plugins that don't support versioning are skipped silently.
func (pm *PluginManager) GetAllVersions() []LoadedPluginInfo {
	pm.mu.RLock()
	names := make([]string, 0, len(pm.plugins))
	for name := range pm.plugins {
		names = append(names, name)
	}
	pm.mu.RUnlock()

	result := make([]LoadedPluginInfo, 0, len(names))
	for _, name := range names {
		info, err := pm.GetVersion(name)
		pm.mu.RLock()
		testRes := pm.testResults[name]
		pm.mu.RUnlock()
		if err != nil {
			result = append(result, LoadedPluginInfo{
				Name:       name,
				Version:    "unknown",
				GitCommit:  "unknown",
				TestResult: testRes,
			})
			continue
		}
		result = append(result, LoadedPluginInfo{
			Name:       info.Name,
			Version:    info.Version,
			GitCommit:  info.GitCommit,
			TestResult: testRes,
		})
	}
	return result
}
