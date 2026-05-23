package plugins

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// =============================================================================
// Enums
// =============================================================================

// PluginSource enumerates where plugins are fetched from.
type PluginSource string

const (
	SourceGitHub PluginSource = "github"
)

var plugins = []PluginDefinition{
	{
		Name:        "captcha-plugin",
		Description: "极验验证码自动识别插件",
		Source:      SourceGitHub,
		RepoOwner:   "firefly001988",
		RepoName:    "biliTicker_gt",
	},
}

// AllPluginSources returns all available plugin sources.
func AllPluginSources() []PluginSource {
	return []PluginSource{SourceGitHub}
}

// =============================================================================
// Plugin definition – describes a known plugin (e.g. captcha-plugin).
// =============================================================================

// PluginDefinition describes a known plugin that the application can download.
type PluginDefinition struct {
	// Name is the plugin binary name, e.g. "captcha-plugin".
	Name string `json:"name"`
	// Description is a human-readable summary of the plugin.
	Description string `json:"description"`
	// Source is where the plugin is fetched from.
	Source PluginSource `json:"source"`
	// RepoOwner is the GitHub repository owner (only for SourceGitHub).
	RepoOwner string `json:"repoOwner"`
	// RepoName is the GitHub repository name (only for SourceGitHub).
	RepoName string `json:"repoName"`
}

// AvailablePlugins returns the list of known plugins.
func AvailablePlugins() []PluginDefinition {
	return plugins
}

// =============================================================================
// Plugin release metadata
// =============================================================================

// PluginInfo describes a single downloadable plugin release.
type PluginInfo struct {
	// Name is the plugin binary name, e.g. "captcha-plugin".
	Name string `json:"name"`
	// Version is the release tag / commit hash.
	Version string `json:"version"`
	// Description from the GitHub release body.
	Description string `json:"description"`
	// PublishedAt is the release publish time (RFC3339).
	PublishedAt string `json:"publishedAt"`
	// Assets lists all downloadable files for this release.
	Assets []PluginAsset `json:"assets"`
	// Source is where this plugin was fetched from.
	Source PluginSource `json:"source"`
}

// PluginAsset is a single downloadable file in a release.
// It wraps the raw asset and adds parsed platform/arch info.
type PluginAsset struct {
	Name          string `json:"name"`
	DownloadURL   string `json:"downloadUrl"`
	Size          int64  `json:"size"`
	Platform      string `json:"platform"`      // windows / linux / darwin
	Arch          string `json:"arch"`          // amd64 / arm64
	PlatformLabel string `json:"platformLabel"` // e.g. "Windows (AMD64)"
	Checksum      string `json:"checksum"`      // checksum for integrity verification
}

// =============================================================================
// Plugin list result
// =============================================================================

// PluginListResult is returned to the frontend.
type PluginListResult struct {
	Plugins []PluginInfo `json:"plugins"`
	Error   string       `json:"error,omitempty"`
}

// =============================================================================
// Fetcher – encapsulates GitHub API calls
// =============================================================================

// Fetcher retrieves plugin metadata from a source.
type Fetcher struct {
	Owner      string
	Repo       string
	PluginName string // binary name, e.g. "captcha-plugin"
	httpClient *http.Client
}

// NewFetcher creates a new plugin Fetcher.
func NewFetcher(owner, repo, pluginName string) *Fetcher {
	return &Fetcher{
		Owner:      owner,
		Repo:       repo,
		PluginName: pluginName,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// =============================================================================
// GitHub API types (unexported)
// =============================================================================

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

// =============================================================================
// Platform parsing regexp
// =============================================================================

// Asset names follow: {name}_{platform}_{arch}_{commit}.tar.gz
// e.g. captcha-plugin_windows_amd64_abc1234.tar.gz
var assetNameRe = regexp.MustCompile(`^(.+)_(windows|linux|darwin)_(amd64|arm64)_[0-9a-fA-F]+\.tar\.gz$`)

func parseAssetName(filename string) (platform, arch string, ok bool) {
	m := assetNameRe.FindStringSubmatch(filename)
	if m == nil {
		return "", "", false
	}
	return m[2], m[3], true
}

func platformLabel(platform, arch string) string {
	labels := map[string]string{
		"windows": "Windows",
		"linux":   "Linux",
		"darwin":  "macOS",
	}
	pl, ok := labels[platform]
	if !ok {
		pl = strings.Title(platform)
	}
	return fmt.Sprintf("%s (%s)", pl, strings.ToUpper(arch))
}

// =============================================================================
// FetchPluginList – fetches releases for all known plugins
// =============================================================================

// FetchPluginList fetches the latest plugin releases for all known plugins
// from their respective sources and returns platform-specific download links.
func FetchPluginList() *PluginListResult {
	allPlugins := make([]PluginInfo, 0)
	for _, def := range AvailablePlugins() {
		f := NewFetcher(def.RepoOwner, def.RepoName, def.Name)
		result := f.fetchPluginList()
		allPlugins = append(allPlugins, result.Plugins...)
	}
	return &PluginListResult{Plugins: allPlugins}
}

// FetchPluginListByName fetches releases for a single plugin identified by name.
func FetchPluginListByName(name string) *PluginListResult {
	for _, def := range AvailablePlugins() {
		if def.Name == name {
			f := NewFetcher(def.RepoOwner, def.RepoName, def.Name)
			return f.fetchPluginList()
		}
	}
	return &PluginListResult{Error: fmt.Sprintf("unknown plugin: %s", name)}
}

// fetchPluginList fetches the latest release for a single plugin.
func (f *Fetcher) fetchPluginList() *PluginListResult {
	releases, err := f.fetchReleases()
	if err != nil {
		return &PluginListResult{Error: fmt.Sprintf("fetch releases for %s: %v", f.PluginName, err)}
	}

	plugins := make([]PluginInfo, 0)

	for _, rel := range releases {
		assets := make([]PluginAsset, 0)

		for _, a := range rel.Assets {
			platform, arch, ok := parseAssetName(a.Name)
			if !ok {
				continue
			}

			checksum := strings.TrimPrefix(a.Digest, "sha256:")

			assets = append(assets, PluginAsset{
				Name:          a.Name,
				DownloadURL:   a.BrowserDownloadURL,
				Size:          a.Size,
				Platform:      platform,
				Arch:          arch,
				PlatformLabel: platformLabel(platform, arch),
				Checksum:      checksum,
			})
		}
		if len(assets) > 0 {
			// Sort assets by platform+arch for consistent ordering.
			sort.Slice(assets, func(i, j int) bool {
				if assets[i].Platform != assets[j].Platform {
					return assets[i].Platform < assets[j].Platform
				}
				return assets[i].Arch < assets[j].Arch
			})
			plugins = append(plugins, PluginInfo{
				Name:        f.PluginName,
				Version:     rel.TagName,
				Description: rel.Body,
				PublishedAt: rel.PublishedAt.Format(time.RFC3339),
				Assets:      assets,
				Source:      SourceGitHub,
			})
		}
	}

	return &PluginListResult{Plugins: plugins}
}

func (f *Fetcher) fetchReleases() ([]ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=5",
		f.Owner, f.Repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "bilibili-ticket-golang-plugin-fetcher")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var releases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	return releases, nil
}
