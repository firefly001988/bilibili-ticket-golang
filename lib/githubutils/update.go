package githubutils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Release represents a single GitHub Release.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Prerelease  bool      `json:"prerelease"`
	CreatedAt   time.Time `json:"created_at"`
	PublishedAt time.Time `json:"published_at"`
}

// UpdateInfo holds the result of a release check.
type UpdateInfo struct {
	// HasUpdate indicates whether a newer release exists.
	HasUpdate bool `json:"hasUpdate"`
	// CurrentVersion is the running app's version (GitCommit, truncated to 7 chars).
	CurrentVersion string `json:"currentVersion"`
	// LatestVersion is the latest release tag name.
	LatestVersion string `json:"latestVersion"`
	// LatestURL is the HTML URL of the latest release page.
	LatestURL string `json:"latestUrl"`
	// PublishedAt is when the latest release was published.
	PublishedAt string `json:"publishedAt"`
	// Assets lists downloadable assets (browser_download_url + name).
	Assets []ReleaseAsset `json:"assets,omitempty"`
}

// ReleaseAsset mirrors a GitHub release asset.
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type githubRelease struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	Prerelease  bool           `json:"prerelease"`
	CreatedAt   time.Time      `json:"created_at"`
	PublishedAt time.Time      `json:"published_at"`
	Assets      []ReleaseAsset `json:"assets"`
}

// Checker fetches releases from a GitHub repository and compares against
// the currently running version (global.GitCommit).
type Checker struct {
	Owner          string // e.g. "firefly001988"
	Repo           string // e.g. "bilibili-ticket-golang"
	CurrentVersion string // e.g. "abc1234" (7-char short hash)
	httpClient     *http.Client
}

// NewChecker creates a new release Checker.
// currentVersion is typically the 7-char short Git commit hash from global.GitCommit.
func NewChecker(owner, repo, currentVersion string) *Checker {
	return &Checker{
		Owner:          owner,
		Repo:           repo,
		CurrentVersion: strings.TrimSpace(currentVersion),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}
}

// CheckForUpdate fetches the latest release and compares it with the current version.
// If HasUpdate is true, the frontend should prompt the user to download.
func (c *Checker) CheckForUpdate() (*UpdateInfo, error) {
	releases, err := c.fetchReleases()
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}

	if len(releases) == 0 {
		return &UpdateInfo{
			HasUpdate:      false,
			CurrentVersion: c.shortCurrent(),
		}, nil
	}

	latest := releases[0] // already sorted newest-first by GitHub API
	info := &UpdateInfo{
		CurrentVersion: c.shortCurrent(),
		LatestVersion:  latest.TagName,
		LatestURL:      latest.HTMLURL,
		PublishedAt:    latest.PublishedAt.Format(time.RFC3339),
		Assets:         latest.Assets,
	}

	// Compare: latest.TagName should be a commit hash.
	// If it differs from our current version, assume newer.
	info.HasUpdate = !c.isSameCommit(latest.TagName)

	return info, nil
}

// isSameCommit returns true if the release tag matches the current commit.
// Tags are expected to be full or short commit hashes.
func (c *Checker) isSameCommit(tag string) bool {
	curr := c.normalizeHash(c.CurrentVersion)
	tagHash := c.normalizeHash(tag)
	if len(curr) == 0 || len(tagHash) == 0 {
		return false
	}
	return strings.HasPrefix(tagHash, curr) || strings.HasPrefix(curr, tagHash)
}

// normalizeHash strips non-hex characters and lowercases.
func (c *Checker) normalizeHash(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	// filter to hex chars only
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// shortCurrent returns the first 7 characters of the current version.
func (c *Checker) shortCurrent() string {
	s := c.CurrentVersion
	if len(s) > 7 {
		s = s[:7]
	}
	return s
}

func (c *Checker) fetchReleases() ([]githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=5",
		c.Owner, c.Repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "bilibili-ticket-golang-updater")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned %d", resp.StatusCode)
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}
