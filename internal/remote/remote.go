// Package remote scans another machine over SSH without installing
// anything there.
//
// The static disk-explorer binary matching the remote OS/architecture is
// streamed through the SSH connection into a private temporary directory,
// run with --export -, and removed. The ncdu-format tree it prints is read
// back locally, so the remote host needs neither Bash, GNU coreutils nor
// Internet access, only a POSIX sh and mktemp.
package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/D1nma/disk_check/internal/export"
	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/updater"
)

// Target is a host and the path to scan on it.
type Target struct {
	Host string // [user@]host, as given to ssh
	Path string // absolute path on the host; "/" by default
}

func (t Target) String() string { return t.Host + ":" + t.Path }

// validHost accepts [user@]host with the characters ssh host names and
// ~/.ssh/config aliases use; no leading '-' (it would be read as an option).
var validHost = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._@-]*$`)

// ParseTarget parses "[user@]host[:/path]".
func ParseTarget(s string) (Target, error) {
	host, path := s, "/"
	if i := strings.IndexByte(s, ':'); i >= 0 {
		host, path = s[:i], s[i+1:]
	}
	if !validHost.MatchString(host) {
		return Target{}, fmt.Errorf("hôte invalide %q (attendu : [utilisateur@]hôte[:/chemin])", host)
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return Target{}, fmt.Errorf("%s : le chemin distant doit être absolu", s)
	}
	if err := checkRemoteArg(path); err != nil {
		return Target{}, err
	}
	return Target{Host: host, Path: filepath.Clean(path)}, nil
}

// checkRemoteArg refuses characters that would need quoting inside the
// remote sh script (it is double-quoted there and single-quoted for the
// login shell, which may be bash, zsh, fish or csh).
func checkRemoteArg(s string) error {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || strings.ContainsRune("'\"`$\\", r) {
			return fmt.Errorf("%q : caractère non pris en charge dans un chemin distant", s)
		}
	}
	return nil
}

// Options controls a remote scan.
type Options struct {
	SSHOpts  []string      // extra ssh(1) options, e.g. -p 2222 -i ~/.ssh/key
	Sudo     bool          // run the scan with sudo -n (passwordless sudo required)
	Mode     string        // "global" or "partition"
	Excludes []string      // absolute paths on the remote host
	Timeout  time.Duration // SSH connect timeout (default 10 s)
	Version  string        // local version: the release whose binaries are pushed
	Binary   string        // explicit binary to push (skips architecture detection)
	CacheDir string        // where downloaded binaries for other architectures are kept
	Status   func(string)  // progress messages (may be nil)
}

// Result is a completed remote scan.
type Result struct {
	Target   Target
	Root     *scanner.Node
	Meta     export.Meta
	Partial  bool   // the remote scan reported unreadable directories (exit code 2)
	Platform string // e.g. "linux/arm64"
	Elapsed  time.Duration
}

// Remote exit codes of the wrapper script.
const (
	exitSetup = 125 // no executable temporary directory, binary not received
)

func (o Options) status(format string, args ...any) {
	if o.Status != nil {
		o.Status(fmt.Sprintf(format, args...))
	}
}

func (o Options) sshArgs(ctlPath string) []string {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	args := []string{
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", int(timeout.Seconds())),
		"-o", "ServerAliveInterval=15",
	}
	if ctlPath != "" {
		args = append(args, "-o", "ControlMaster=auto", "-o", "ControlPath="+ctlPath, "-o", "ControlPersist=60")
	}
	return append(args, o.SSHOpts...)
}

// Scan runs disk-explorer on the target host and returns the tree.
func Scan(ctx context.Context, t Target, o Options) (*Result, error) {
	start := time.Now()
	for _, ex := range o.Excludes {
		if err := checkRemoteArg(ex); err != nil {
			return nil, err
		}
	}
	if o.Mode != "" && o.Mode != "global" && o.Mode != "partition" {
		return nil, fmt.Errorf("mode invalide %q", o.Mode)
	}

	// One SSH connection for the probe and the scan (ControlMaster).
	ctlDir, err := os.MkdirTemp("", "disk-explorer-ssh-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(ctlDir)
	ctlPath := filepath.Join(ctlDir, "c")
	sshBase := o.sshArgs(ctlPath)
	defer exec.Command("ssh", append(append([]string{}, sshBase...), "-O", "exit", t.Host)...).Run()

	o.status("%s : connexion…", t.Host)
	goos, goarch, err := probe(ctx, t.Host, sshBase)
	if err != nil {
		return nil, err
	}
	platform := goos + "/" + goarch

	bin := o.Binary
	if bin == "" {
		bin, err = binaryFor(goos, goarch, o)
		if err != nil {
			return nil, err
		}
	}
	f, err := os.Open(bin)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil {
		o.status("%s (%s) : envoi du binaire (%.1f MiB) puis scan de %s…", t.Host, platform, float64(fi.Size())/(1<<20), t.Path)
	}

	cmd := exec.CommandContext(ctx, "ssh", append(append(append([]string{}, sshBase...), "--", t.Host), remoteCommand(t, o))...)
	cmd.Stdin = f
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 64 << 10}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ssh : %w", err)
	}
	root, meta, readErr := export.ReadNcdu(stdout)
	io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()

	code := 0
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		code = ee.ExitCode()
	} else if waitErr != nil {
		return nil, fmt.Errorf("%s : %w", t.Host, waitErr)
	}
	msg := strings.TrimSpace(stderr.String())
	switch {
	case code == 255:
		return nil, fmt.Errorf("%s : connexion SSH impossible : %s", t.Host, firstLine(msg))
	case code == exitSetup:
		return nil, fmt.Errorf("%s : préparation distante impossible : %s", t.Host, firstLine(msg))
	case code != 0 && code != 2:
		if msg == "" {
			msg = fmt.Sprintf("code de sortie %d", code)
		}
		return nil, fmt.Errorf("%s : échec du scan distant : %s", t.Host, firstLine(msg))
	case readErr != nil:
		return nil, fmt.Errorf("%s : réponse illisible : %v %s", t.Host, readErr, firstLine(msg))
	}
	if meta.Host == "" {
		meta.Host = t.Host
	}
	return &Result{Target: t, Root: root, Meta: meta, Partial: code == 2, Platform: platform, Elapsed: time.Since(start)}, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// probe asks the host for its OS and architecture.
func probe(ctx context.Context, host string, sshBase []string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "ssh", append(append(append([]string{}, sshBase...), "--", host), "uname -sm")...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", "", fmt.Errorf("%s : connexion SSH impossible : %s", host, firstLine(msg))
	}
	return platformFromUname(strings.TrimSpace(out.String()))
}

// platformFromUname maps `uname -sm` output to Go's GOOS/GOARCH, with the
// same table as install.sh.
func platformFromUname(s string) (string, string, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return "", "", fmt.Errorf("réponse inattendue de uname : %q", s)
	}
	var goos string
	switch f[0] {
	case "Linux":
		goos = "linux"
	case "Darwin":
		goos = "darwin"
	default:
		return "", "", fmt.Errorf("système distant non pris en charge : %s", f[0])
	}
	var goarch string
	switch m := f[1]; {
	case m == "x86_64" || m == "amd64":
		goarch = "amd64"
	case m == "aarch64" || m == "arm64":
		goarch = "arm64"
	case m == "i386" || m == "i486" || m == "i586" || m == "i686" || m == "x86":
		goarch = "386"
	case strings.HasPrefix(m, "armv") || m == "arm":
		goarch = "arm"
	case m == "riscv64":
		goarch = "riscv64"
	case m == "ppc64le":
		goarch = "ppc64le"
	case m == "s390x":
		goarch = "s390x"
	case m == "loongarch64":
		goarch = "loong64"
	case m == "mips":
		goarch = "mips"
	case m == "mipsel" || m == "mipsle":
		goarch = "mipsle"
	case m == "mips64":
		goarch = "mips64"
	case m == "mips64el" || m == "mips64le":
		goarch = "mips64le"
	default:
		return "", "", fmt.Errorf("architecture distante non prise en charge : %s", m)
	}
	return goos, goarch, nil
}

// binaryFor returns a local binary that runs on goos/goarch: this very
// executable when the platform matches, else the release binary for that
// platform from the cache (downloaded and SHA256-verified on first use).
func binaryFor(goos, goarch string, o Options) (string, error) {
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		return os.Executable()
	}
	if !strings.HasPrefix(o.Version, "v") {
		return "", fmt.Errorf("l'hôte est en %s/%s : une version de développement ne peut pas télécharger ce binaire ; compilez-le (GOOS=%s GOARCH=%s CGO_ENABLED=0 go build ./cmd/disk-explorer) et passez-le avec --ssh-binary", goos, goarch, goos, goarch)
	}
	dir := o.CacheDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "disk-explorer", "bin", o.Version)
	}
	path := filepath.Join(dir, fmt.Sprintf("disk-explorer-%s-%s", goos, goarch))
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	o.status("téléchargement du binaire %s/%s (%s)…", goos, goarch, o.Version)
	tmp := path + ".tmp"
	if err := updater.FetchBinary(o.Version, goos, goarch, tmp); err != nil {
		return "", fmt.Errorf("binaire %s/%s : %w", goos, goarch, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// remoteCommand builds the command line given to ssh. The remote login
// shell may be anything, so the actual work is a POSIX sh script passed as
// one single-quoted argument; the script itself contains no single quote
// and no backslash, and user values were checked by checkRemoteArg.
//
// The binary arrives on stdin and is written once; if the directory turns
// out to be mounted noexec, it is copied to the next candidate.
func remoteCommand(t Target, o Options) string {
	args := []string{"--export", "-", "--no-snapshot"}
	if o.Mode != "" {
		args = append(args, "--mode", o.Mode)
	}
	for _, ex := range o.Excludes {
		args = append(args, "--exclude", ex)
	}
	args = append(args, "--", t.Path)
	var quoted []string
	for _, a := range args {
		quoted = append(quoted, `"`+a+`"`)
	}
	run := `"$d/de" ` + strings.Join(quoted, " ")
	if o.Sudo {
		run = `sudo -n ` + run
	}
	script := strings.Join([]string{
		`umask 077`,
		`src=`,
		`for base in "${TMPDIR:-/tmp}" /var/tmp /dev/shm "$HOME"; do`,
		`  d=$(mktemp -d "$base/disk-explorer.XXXXXX" 2>/dev/null) || continue`,
		`  if [ -z "$src" ]; then cat > "$d/de" || { rm -rf "$d"; exit 125; }; src="$d/de";`,
		`  else cp "$src" "$d/de" 2>/dev/null || { rm -rf "$d"; continue; }; fi`,
		`  chmod 700 "$d/de"`,
		`  if "$d/de" --version >/dev/null 2>&1; then`,
		`    if [ "$src" != "$d/de" ]; then rm -rf "${src%/de}"; fi`,
		`    trap "rm -rf $d; exit 129" HUP INT TERM`,
		`    ` + run + ` </dev/null; rc=$?`,
		`    rm -rf "$d"; exit $rc`,
		`  fi`,
		`  if [ "$src" != "$d/de" ]; then rm -rf "$d"; fi`,
		`done`,
		`if [ -n "$src" ]; then rm -rf "${src%/de}"; fi`,
		`echo "disk-explorer : aucun dossier temporaire exécutable (noexec ?) ni mktemp" >&2`,
		`exit 125`,
	}, "\n")
	return "sh -c '" + script + "'"
}

// limitedWriter keeps at most n bytes (remote stderr can be chatty).
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	total := len(p)
	if l.n <= 0 {
		return total, nil
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	n, err := l.w.Write(p)
	l.n -= n
	if err != nil {
		return n, err
	}
	return total, nil
}
