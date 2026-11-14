package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// repoURL is a var so tests can point SelfUpdate at a local server.
var repoURL = "https://github.com/D1nma/disk_check"

const apiURL = "https://api.github.com/repos/D1nma/disk_check/releases/latest"

// releaseTag accepts vX.Y.Z (optionally with a -suffix). The tag comes from the
// GitHub API and is used to build a cache path, so anything else is refused.
var releaseTag = regexp.MustCompile(`^v[0-9]+(\.[0-9]+)*(-[0-9A-Za-z.]+)?$`)

// LatestRelease fetches the latest release tag from GitHub.
func LatestRelease() (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "disk-explorer")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return payload.TagName, nil
}

// UpdateAvailable returns (latestTag, true) when a strictly newer version
// exists. Returns ("", false) for dev builds or when the check fails.
func UpdateAvailable(current string) (string, bool) {
	if current == "dev" || current == "" || !strings.HasPrefix(current, "v") {
		return "", false
	}
	latest, err := LatestRelease()
	if err != nil || latest == "" || !Newer(latest, current) {
		return latest, false
	}
	return latest, true
}

// CheckEnabled reports whether the background update check may contact
// GitHub: it is off when DISK_EXPLORER_NO_UPDATE is set (air-gapped
// servers, package-managed installs, privacy).
func CheckEnabled() bool {
	return os.Getenv("DISK_EXPLORER_NO_UPDATE") == ""
}

// CanSelfUpdate reports whether the running binary can be replaced in place
// (its directory is writable). When it cannot (root-owned /usr/local/bin,
// package manager), the TUI only announces the release.
func CanSelfUpdate() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	f, err := os.CreateTemp(filepath.Dir(exe), ".disk-explorer-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// Newer reports whether release tag a is a strictly higher version than b
// (vMAJOR.MINOR.PATCH[-pre]; a pre-release sorts before its release). Tags
// that do not parse are never considered newer, so a malformed answer from
// the API cannot trigger a "downgrade".
func Newer(a, b string) bool {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := 0; i < 3; i++ {
		if va.num[i] != vb.num[i] {
			return va.num[i] > vb.num[i]
		}
	}
	switch {
	case va.pre == vb.pre:
		return false
	case va.pre == "":
		return true // release > its pre-release
	case vb.pre == "":
		return false
	}
	return va.pre > vb.pre
}

type version struct {
	num [3]int
	pre string
}

func parseVersion(s string) (version, bool) {
	var v version
	if !releaseTag.MatchString(s) {
		return v, false
	}
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n := 0
		for _, c := range p {
			n = n*10 + int(c-'0')
			if n > 1<<24 {
				return v, false
			}
		}
		v.num[i] = n
	}
	return v, true
}

// Prefetch downloads the release binary for tag, verifies it against the
// release SHA256SUMS, and leaves it as a tempfile next to the running
// executable so Install can swap it in with an atomic rename. It does not
// touch the running binary.
func Prefetch(tag string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(exe), "disk-explorer-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := tmpFile.Name()
	tmpFile.Close()
	if err := FetchBinary(tag, runtime.GOOS, runtime.GOARCH, tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// FetchBinary downloads the release binary of tag for goos/goarch into
// dest (overwritten), verifies it against the release SHA256SUMS and makes
// it executable. dest is removed on failure. Used for self-updates and to
// push the right binary to a remote host of another architecture.
func FetchBinary(tag, goos, goarch, dest string) error {
	if !releaseTag.MatchString(tag) {
		return fmt.Errorf("invalid release tag %q", tag)
	}
	name := fmt.Sprintf("disk-explorer-%s-%s", goos, goarch)
	binURL := fmt.Sprintf("%s/releases/download/%s/%s", repoURL, tag, name)
	sumsURL := fmt.Sprintf("%s/releases/download/%s/SHA256SUMS", repoURL, tag)

	if err := downloadFile(binURL, dest); err != nil {
		os.Remove(dest)
		return fmt.Errorf("download: %w", err)
	}
	sums, err := fetchText(sumsURL)
	if err != nil {
		os.Remove(dest)
		return fmt.Errorf("checksum list unavailable: %w", err)
	}
	if err := verifyChecksum(dest, name, sums); err != nil {
		os.Remove(dest)
		return fmt.Errorf("checksum: %w", err)
	}
	if err := os.Chmod(dest, 0755); err != nil {
		os.Remove(dest)
		return err
	}
	return nil
}

// Install atomically replaces the running executable with the prefetched,
// already-verified file. The running process keeps its old inode (Linux and
// macOS semantics), so this is safe to call from inside the app.
func Install(file string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return installTo(file, exe)
}

func installTo(file, dest string) error {
	if err := os.Chmod(file, 0755); err != nil {
		return err
	}
	return os.Rename(file, dest)
}

// SelfUpdate downloads, verifies and installs the release for tag over the
// running executable. The caller decides whether to restart the process.
func SelfUpdate(tag string) error {
	tmp, err := Prefetch(tag)
	if err != nil {
		return err
	}
	if err := Install(tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	fmt.Fprintf(os.Stderr, "Mise à jour installée : relancez disk-explorer pour utiliser %s\n", tag)
	return nil
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "disk-explorer")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d pour %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(dest)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(dest)
		return err
	}
	return nil
}

func fetchText(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d pour %s", resp.StatusCode, url)
	}
	// The sums file is a few hundred bytes; cap the read so a bad server cannot fill memory.
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b), err
}

func verifyChecksum(path, name, sums string) error {
	expected := ""
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("no checksum entry for %s", name)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("SHA256 attendu %s, obtenu %s", expected, actual)
	}
	return nil
}
