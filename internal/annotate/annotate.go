// Package annotate explains well-known paths of a Linux (or macOS) host.
//
// It turns opaque container-storage directories into readable names
// ("/var/lib/docker/overlay2/3f9a…" becomes "conteneur postgres-prod
// (couche d'écriture)", "/var/lib/kubelet/pods/<uid>" becomes
// "pod default/api-7f9c") and, for well-known space hogs, suggests the
// proper cleanup command instead of a blind rm -rf.
//
// An Annotator is cheap to create and safe for concurrent use. Each metadata
// source (Docker layer database, podman containers.json, /var/log/pods, ...)
// is read lazily, at most once, the first time a lookup needs it, and every
// result is cached per path. Only small, specific metadata files are read and
// directories are listed one level deep with a bounded number of entries; no
// external command is run and /proc is never read. A missing or unreadable
// file simply yields no annotation, or a more generic one.
package annotate

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unicode/utf8"

	"github.com/D1nma/disk_check/internal/textsafe"
)

// Annotation describes a well-known path.
type Annotation struct {
	Label   string // short French description shown next to the name, e.g. "conteneur postgres-prod (couche d'écriture)"
	Cleanup string // suggested command or advice, "" if none, e.g. "journalctl --vacuum-size=500M"
	Regen   bool   // cache / regenerable artifact: deleting it is safe, it will be rebuilt or re-downloaded
}

// Options configures an Annotator.
type Options struct {
	Root string // prefix where host metadata files are read from ("" means "/"); for tests
}

// Annotator resolves annotations for host paths. The zero value is not
// usable; create one with New. A nil *Annotator is valid and annotates
// nothing.
type Annotator struct {
	root   string
	darwin bool // map /private/{var,tmp,etc} to /{var,tmp,etc} before matching

	// reads counts metadata I/O operations (file reads, directory listings,
	// stats). Tests use it to check that metadata is loaded only once.
	reads atomic.Int64

	mu    sync.RWMutex
	cache map[cacheKey]cacheEntry

	dockerRootsOnce sync.Once
	dockerRoots     []string

	storesMu sync.Mutex
	docker   map[string]*dockerStore // keyed by data root
	podman   map[string]*podmanStore // keyed by storage root

	podsOnce sync.Once
	pods     map[string]podRef // pod UID -> namespace/name
}

type cacheKey struct {
	path  string
	isDir bool
}

type cacheEntry struct {
	ann Annotation
	ok  bool
}

// Bounds on the work and memory an Annotator may spend.
const (
	maxCacheEntries = 8192     // per-path results kept before the cache is reset
	maxDirEntries   = 16384    // entries read from one metadata directory listing
	maxIDFile       = 512      // mount-id, cache-id: one hex ID
	maxConfigFile   = 4 << 20  // daemon.json, config.v2.json, image configs
	maxIndexFile    = 16 << 20 // repositories.json, containers.json, layers.json
	maxImages       = 4096     // image configs read to map layers to tags
	maxNameLen      = 64       // bytes of a metadata-provided name kept in a label
)

// New returns an Annotator reading host metadata under opts.Root.
func New(opts Options) *Annotator {
	root := opts.Root
	if root == "" {
		root = "/"
	}
	return &Annotator{
		root:   filepath.Clean(root),
		darwin: runtime.GOOS == "darwin",
		cache:  make(map[cacheKey]cacheEntry),
		docker: make(map[string]*dockerStore),
		podman: make(map[string]*podmanStore),
	}
}

// Lookup returns the annotation for absPath, an absolute path on the scanned
// host (NOT prefixed by Root). isDir tells whether absPath is a directory:
// some rules only apply to files (rotated logs, container logs) and most only
// to directories. The second result is false when nothing is known about the
// path.
func (a *Annotator) Lookup(absPath string, isDir bool) (Annotation, bool) {
	if a == nil {
		return Annotation{}, false
	}
	p, ok := a.normalize(absPath)
	if !ok {
		return Annotation{}, false
	}
	key := cacheKey{path: p, isDir: isDir}

	a.mu.RLock()
	e, hit := a.cache[key]
	a.mu.RUnlock()
	if hit {
		return e.ann, e.ok
	}

	ann, found := a.evaluate(newQuery(p, isDir))

	a.mu.Lock()
	if len(a.cache) >= maxCacheEntries {
		clear(a.cache)
	}
	a.cache[key] = cacheEntry{ann: ann, ok: found}
	a.mu.Unlock()
	return ann, found
}

// normalize cleans absPath and rejects relative paths. On macOS, /var, /tmp
// and /etc are symlinks into /private, so a scan of "/" reports
// /private/var/log/...; those are matched as /var/log/....
func (a *Annotator) normalize(absPath string) (string, bool) {
	if absPath == "" || absPath[0] != '/' {
		return "", false
	}
	p := filepath.Clean(absPath)
	if a.darwin {
		if rest, ok := strings.CutPrefix(p, "/private"); ok {
			for _, d := range [...]string{"/var", "/tmp", "/etc"} {
				if within(rest, d) {
					return rest, true
				}
			}
		}
	}
	return p, true
}

// query is the path being annotated, pre-split for the rules.
type query struct {
	path  string // cleaned absolute path
	isDir bool
	name  string // last path component
	dir   string // parent directory
}

func newQuery(p string, isDir bool) *query {
	i := strings.LastIndexByte(p, '/')
	dir := p[:i]
	if dir == "" {
		dir = "/"
	}
	return &query{path: p, isDir: isDir, name: p[i+1:], dir: dir}
}

// rule is one family of path patterns. Families are tried in order and the
// first match wins, so more specific families come first: container storage
// (whose metadata names opaque directories) before the generic system table,
// and the "anywhere" developer artifacts last. Within a family, the patterns
// themselves live in data tables (system.go) or in per-store resolvers
// (containers.go).
type rule struct {
	name  string
	match func(a *Annotator, q *query) (Annotation, bool)
}

var rules = []rule{
	{"docker", (*Annotator).matchDocker},
	{"podman", (*Annotator).matchPodman},
	{"containerd", (*Annotator).matchContainerd},
	{"kubernetes", (*Annotator).matchKubernetes},
	{"system", (*Annotator).matchSystem},
	{"rotated-log", (*Annotator).matchRotatedLog},
	{"home", (*Annotator).matchHome},
	{"dev-artifact", (*Annotator).matchDevArtifact},
}

func (a *Annotator) evaluate(q *query) (Annotation, bool) {
	for _, r := range rules {
		if ann, ok := r.match(a, q); ok {
			return ann, true
		}
	}
	return Annotation{}, false
}

// --- path helpers ---------------------------------------------------------

// within reports whether p is root or lies below it. Both are cleaned paths,
// either both absolute or both relative.
func within(p, root string) bool {
	return p == root || (len(p) > len(root) && p[len(root)] == '/' && strings.HasPrefix(p, root))
}

// under returns the components of p below root (nil for root itself) and
// whether p is within root.
func under(p, root string) ([]string, bool) {
	if !within(p, root) {
		return nil, false
	}
	if len(p) == len(root) {
		return nil, true
	}
	return strings.Split(p[len(root)+1:], "/"), true
}

// homePrefixes are the parents of per-user home directories.
var homePrefixes = [...]string{"/home/", "/Users/", "/var/home/"}

// splitHome splits p into a home directory (/root, /home/<u>, /Users/<u>,
// /var/home/<u>) and the path relative to it ("" for the home itself).
func splitHome(p string) (home, rel string, ok bool) {
	if within(p, "/root") {
		return "/root", strings.TrimPrefix(p[len("/root"):], "/"), true
	}
	for _, prefix := range homePrefixes {
		rest, found := strings.CutPrefix(p, prefix)
		if !found {
			continue
		}
		user, rel, _ := strings.Cut(rest, "/")
		if user == "" {
			return "", "", false
		}
		return prefix + user, rel, true
	}
	return "", "", false
}

// --- bounded metadata I/O -------------------------------------------------

var errSkip = errors.New("annotate: not a small regular file")

func (a *Annotator) hostPath(p string) string { return filepath.Join(a.root, p) }

// openNoBlock opens a host path read-only without blocking on FIFOs.
func (a *Annotator) openNoBlock(p string) (*os.File, error) {
	return os.OpenFile(a.hostPath(p), os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// readSmall returns the content of the regular file p (a host path, not
// prefixed by Root) if it is at most limit bytes long.
func (a *Annotator) readSmall(p string, limit int64) ([]byte, error) {
	a.reads.Add(1)
	f, err := a.openNoBlock(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errSkip
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errSkip
	}
	return b, nil
}

// listNames returns at most maxDirEntries entry names of the directory p.
func (a *Annotator) listNames(p string) []string {
	a.reads.Add(1)
	f, err := a.openNoBlock(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	names, _ := f.Readdirnames(maxDirEntries)
	return names
}

// isFile reports whether the host path p is (or links to) a regular file.
func (a *Annotator) isFile(p string) bool {
	a.reads.Add(1)
	st, err := os.Stat(a.hostPath(p))
	return err == nil && st.Mode().IsRegular()
}

// --- string helpers -------------------------------------------------------

// displayName makes a metadata-provided name (container, pod, volume, image
// tag: all attacker-influenced) safe and short enough for a label.
func displayName(s string) string {
	s = textsafe.Clean(s)
	if len(s) <= maxNameLen {
		if s == "" {
			return "?"
		}
		return s
	}
	cut := maxNameLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// isSafeArg reports whether s can be pasted unquoted into a shell command:
// the character set of Docker container and volume names.
func isSafeArg(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case i > 0 && (c == '_' || c == '.' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// shellPath returns p ready to paste into a shell command, or "" if p holds
// characters that would not survive textsafe.Clean (the command would then
// not name the real file).
func shellPath(p string) string {
	if textsafe.Clean(p) != p {
		return ""
	}
	safe := true
	for i := 0; i < len(p); i++ {
		c := p[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("/._-+:@%,=", c) >= 0) {
			safe = false
			break
		}
	}
	if safe {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// isHexID reports whether s looks like a content-addressed ID (Docker
// container, layer, overlay2 directory): at least 12 lowercase hex digits.
func isHexID(s string) bool {
	if len(s) < 12 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
