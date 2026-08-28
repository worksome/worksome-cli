// Package update checks whether a newer release of the CLI is available.
//
// The passive notice is deliberately conservative: it only ever appears on an
// interactive terminal, at most once a day, and never blocks the command it is
// attached to. Agents, pipelines and containers see nothing.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// releasesURL is a var so tests can point it at a local server.
var releasesURL = "https://api.github.com/repos/worksome/worksome-cli/releases/latest"

// FetchTimeout bounds the background release check. The caller must wait at
// least this long before exiting, or it will kill the goroutine before the
// result is cached and the cache will never warm.
const FetchTimeout = 2 * time.Second

const (
	// checkInterval is how long a result is reused before asking again.
	checkInterval = 24 * time.Hour
	// cacheFile lives beside the config so it inherits the same 0700 directory.
	cacheFile = "update-check.json"
)

// Release is the subset of the GitHub release payload we care about.
type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

type cache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// Fetch asks GitHub for the latest release. It is the uncached path, used by
// `worksome version --check`.
func Fetch(ctx context.Context, client *http.Client) (*Release, error) {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Surfaced to `version --check`, where the user asked and deserves to
		// know the answer is unavailable rather than see a bogus "up to date".
		// Unauthenticated calls are rate limited per IP, so this is the common
		// failure; the passive path swallows it in LatestCached.
		return nil, fmt.Errorf("checking for updates: %s", resp.Status)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("checking for updates: empty tag in response")
	}
	return &rel, nil
}

// IsNewer reports whether latest is strictly newer than current.
//
// Ordering matters rather than mere inequality: a development build reports
// "dev", and someone running a build ahead of the latest release has nothing
// to do. Anything that doesn't parse as a dotted numeric version — a
// pre-release tag, say — returns false, because a courtesy notice is the wrong
// place to guess.
func IsNewer(current, latest string) bool {
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	return compare(l, c) > 0
}

// parseVersion splits a dotted numeric version into its parts. It reports
// false for "dev", empty strings, and anything carrying a suffix.
func parseVersion(v string) ([]int, bool) {
	v = normalise(v)
	if v == "" {
		return nil, false
	}
	fields := strings.Split(v, ".")
	parts := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return nil, false
		}
		parts = append(parts, n)
	}
	return parts, true
}

// compare returns >0 when a is newer than b, 0 when equal, <0 otherwise.
// Missing trailing components count as zero, so 1.2 == 1.2.0.
func compare(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x - y
		}
	}
	return 0
}

func normalise(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// Suppressed reports whether the passive check must not run.
//
// stdout and stderr must both be terminals: a notice written while output is
// piped would land in a scripted consumer's stream, and an agent reading JSON
// should never see it. CI and the opt-out variable are honoured on top.
func Suppressed(currentVersion string, stdoutTTY, stderrTTY bool) bool {
	if os.Getenv("WORKSOME_NO_UPDATE_CHECK") != "" {
		return true
	}
	if os.Getenv("CI") != "" {
		return true
	}
	if normalise(currentVersion) == "dev" {
		return true
	}
	return !stdoutTTY || !stderrTTY
}

// Notice renders the one-line notice, including the upgrade command for the
// install method actually in use.
func Notice(current, latest string) string {
	return fmt.Sprintf("\nA new release of worksome is available: %s → %s\n%s\n",
		normalise(current), normalise(latest), UpgradeHint())
}

// UpgradeHint returns the correct upgrade command for how this binary was
// installed, so the advice can't be wrong for the reader.
func UpgradeHint() string {
	exe, err := os.Executable()
	if err != nil {
		return downloadURL
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return hintForPath(exe)
}

const downloadURL = "https://github.com/worksome/worksome-cli/releases/latest"

// hintForPath classifies an already-resolved executable path. Split out from
// UpgradeHint so it can be tested without moving the test binary around.
func hintForPath(exe string) string {
	switch {
	case strings.Contains(exe, string(filepath.Separator)+"Caskroom"+string(filepath.Separator)):
		return "brew upgrade --cask worksome"
	case strings.Contains(exe, string(filepath.Separator)+"Cellar"+string(filepath.Separator)):
		return "brew upgrade worksome"
	case gopathBin(exe):
		return "go install github.com/worksome/worksome-cli/cmd/worksome@latest"
	default:
		return downloadURL
	}
}

func gopathBin(exe string) bool {
	dir := os.Getenv("GOPATH")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, "go")
	}
	binDir := filepath.Join(dir, "bin")
	// The executable path has already been through EvalSymlinks, so resolve
	// this side too or a symlinked prefix (macOS /tmp -> /private/tmp) never
	// matches.
	if resolved, err := filepath.EvalSymlinks(binDir); err == nil {
		binDir = resolved
	}
	return strings.HasPrefix(exe, binDir+string(filepath.Separator))
}

// cachePath returns the cache location, or "" when there is no home directory
// to put it in. A container with no HOME simply doesn't cache.
func cachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".worksome", cacheFile)
}

// readCache returns the cached latest version when it is still fresh.
func readCache() (string, bool) {
	path := cachePath()
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		return "", false
	}
	if time.Since(c.CheckedAt) > checkInterval {
		return "", false
	}
	return c.LatestVersion, true
}

// writeCache records the result. Every failure is ignored: a read-only or
// absent home directory must not turn a courtesy check into an error.
func writeCache(latest string) {
	path := cachePath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}
	data, err := json.Marshal(cache{CheckedAt: time.Now(), LatestVersion: latest})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}

// LatestCached returns the latest known version, consulting the network only
// when the cached value has expired. It never returns an error: the caller is
// rendering a courtesy notice, not doing work the user asked for.
func LatestCached(ctx context.Context, client *http.Client) string {
	if v, ok := readCache(); ok {
		return v
	}
	rel, err := Fetch(ctx, client)
	if err != nil {
		// Cache the failure as an empty result so a broken network doesn't mean
		// a request on every single invocation for the next 24 hours.
		writeCache("")
		return ""
	}
	writeCache(rel.TagName)
	return rel.TagName
}
