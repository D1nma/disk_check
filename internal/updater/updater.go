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

// UpdateAvailable returns (latestTag, true) when a newer version exists.
// Returns ("", false) for dev builds or when the check fails.
func UpdateAvailable(current string) (string, bool) {
	if current == "dev" || current == "" || !strings.HasPrefix(current, "v") {
		return "", false
	}
	latest, err := LatestRelease()
	if err != nil || latest == "" || latest == current {
		return latest, false
	}
	return latest, true
}

// SelfUpdate downloads the binary for tag and stores it in the cache directory.
func SelfUpdate(tag string) error {
	if !releaseTag.MatchString(tag) {
		return fmt.Errorf("invalid release tag %q", tag)
	}
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	name := fmt.Sprintf("disk-explorer-%s-%s", goos, goarch)
	binURL := fmt.Sprintf("%s/releases/download/%s/%s", repoURL, tag, name)
	sumsURL := fmt.Sprintf("%s/releases/download/%s/SHA256SUMS", repoURL, tag)

	cacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "disk-explorer", "bin", tag)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	target := filepath.Join(cacheDir, "disk-explorer")
	tmp := target + ".tmp"

	fmt.Fprintf(os.Stderr, "Téléchargement de disk-explorer %s (%s/%s)...\n", tag, goos, goarch)
	if err := downloadFile(binURL, tmp); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	sums, err := fetchText(sumsURL)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("checksum list unavailable: %w", err)
	}
	if err := verifyChecksum(tmp, name, sums); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("checksum: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Checksum OK")

	if err := os.Chmod(tmp, 0755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	fmt.Fprintf(os.Stderr, "Mise à jour installée : %s\n", target)
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
	b, err := io.ReadAll(resp.Body)
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
