// Package snapshot keeps a compact history of scans so disk-explorer can
// answer "what grew since last time?".
//
// After each complete scan, the sizes of every directory and file of at
// least MinBytes are written (gzip, a few hundred KiB for a whole server)
// under the user's state directory. The next scan of the same root loads the
// latest snapshot to show a growth column, and to estimate how many entries
// the scan will visit (progress percentage).
package snapshot

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/D1nma/disk_check/internal/scanner"
)

// MinBytes is the smallest directory or file recorded in a snapshot.
const MinBytes = 1 << 20

// Keep is how many snapshots are kept per scanned root.
const Keep = 20

const magic = "DXSNAP1"

// Snapshot is the recorded state of one scan.
type Snapshot struct {
	ID      string           // identity of the scanned root, e.g. "/var" or "host:/var"
	Mode    string           // "global" or "partition"
	Time    time.Time        // when the scan finished
	Entries int64            // files + directories visited
	Size    int64            // root size
	Sizes   map[string]int64 // relative path ("" = root) → size, entries >= MinBytes
}

// FromTree records root. id names the scanned root (the absolute path, or
// "host:/path" for remote scans).
func FromTree(root *scanner.Node, id, mode string, entries int64, at time.Time) *Snapshot {
	s := &Snapshot{ID: id, Mode: mode, Time: at, Entries: entries, Sizes: make(map[string]int64)}
	if root == nil {
		return s
	}
	s.Size = root.Size
	s.Sizes[""] = root.Size
	var walk func(n *scanner.Node, rel string)
	walk = func(n *scanner.Node, rel string) {
		for _, c := range n.Children {
			if c.Size < MinBytes {
				continue
			}
			p := c.Name
			if rel != "" {
				p = rel + "/" + c.Name
			}
			s.Sizes[p] = c.Size
			if c.IsDir {
				walk(c, p)
			}
		}
	}
	walk(root, "")
	return s
}

// RelPath returns n's path relative to the scan root, as used in Sizes.
func RelPath(n *scanner.Node) string {
	if n == nil || n.Parent == nil {
		return ""
	}
	var parts []string
	for c := n; c.Parent != nil; c = c.Parent {
		parts = append(parts, c.Name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/")
}

// Delta compares n with its size in s. exact is false when n was not
// recorded (new, or smaller than MinBytes last time): delta is then n.Size,
// an upper bound.
func (s *Snapshot) Delta(n *scanner.Node) (delta int64, exact bool) {
	if s == nil || n == nil {
		return 0, false
	}
	prev, ok := s.Sizes[RelPath(n)]
	if !ok {
		if n.Size < MinBytes {
			return 0, true // below the threshold now and then: nothing to report
		}
		return n.Size, false
	}
	return n.Size - prev, true
}

// Growth is one entry of a growth ranking.
type Growth struct {
	Path  string // relative to the root
	Delta int64
	Size  int64
	Exact bool
}

// TopGrowth ranks the directories and files of root that grew the most
// since s (direct children of a grown directory are ranked too, so the
// list points at the actual culprit, not only at the top-level folder).
func (s *Snapshot) TopGrowth(root *scanner.Node, limit int) []Growth {
	if s == nil || root == nil || limit <= 0 {
		return nil
	}
	var out []Growth
	var walk func(n *scanner.Node)
	walk = func(n *scanner.Node) {
		for _, c := range n.Children {
			d, exact := s.Delta(c)
			if d <= 0 {
				continue
			}
			out = append(out, Growth{Path: RelPath(c), Delta: d, Size: c.Size, Exact: exact})
			if c.IsDir {
				walk(c)
			}
		}
	}
	walk(root)
	// Keep the deepest entry when a parent grew by (almost) the same amount:
	// "/var +3 GiB" and "/var/log +3 GiB" say the same thing.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Delta != out[j].Delta {
			return out[i].Delta > out[j].Delta
		}
		return out[i].Path < out[j].Path
	})
	var res []Growth
	for _, g := range out {
		redundant := false
		for k, r := range res {
			if strings.HasPrefix(g.Path, r.Path+"/") && g.Delta*10 >= r.Delta*9 {
				res[k] = g // the child explains (nearly) all of the parent's growth
				redundant = true
				break
			}
		}
		if !redundant {
			res = append(res, g)
		}
		if len(res) >= limit*2 {
			break
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Delta > res[j].Delta })
	if len(res) > limit {
		res = res[:limit]
	}
	return res
}

// Store reads and writes snapshots under Dir.
type Store struct {
	Dir string
}

// Enabled reports whether snapshots are wanted (DISK_EXPLORER_NO_SNAPSHOT unset).
func Enabled() bool {
	return os.Getenv("DISK_EXPLORER_NO_SNAPSHOT") == ""
}

// DefaultStore returns the store under $DISK_EXPLORER_STATE_DIR, else
// $XDG_STATE_HOME/disk-explorer, else ~/.local/state/disk-explorer.
func DefaultStore() (Store, error) {
	if d := os.Getenv("DISK_EXPLORER_STATE_DIR"); d != "" {
		return Store{Dir: filepath.Join(d, "snapshots")}, nil
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return Store{Dir: filepath.Join(d, "disk-explorer", "snapshots")}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Dir: filepath.Join(home, ".local", "state", "disk-explorer", "snapshots")}, nil
}

func (st Store) keyDir(id, mode string) string {
	h := sha256.Sum256([]byte(id + "\x00" + mode))
	return filepath.Join(st.Dir, hex.EncodeToString(h[:8]))
}

// Save writes s atomically and prunes old snapshots of the same root.
func (st Store) Save(s *Snapshot) error {
	dir := st.keyDir(s.ID, s.Mode)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := filepath.Join(dir, strconv.FormatInt(s.Time.UnixNano(), 10)+".snap.gz")
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := write(tmp, s); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		return err
	}
	return st.prune(dir)
}

func (st Store) prune(dir string) error {
	files, err := st.list(dir)
	if err != nil {
		return err
	}
	for i := 0; i+Keep < len(files); i++ {
		os.Remove(files[i])
	}
	return nil
}

// list returns the snapshot files of dir, oldest first.
func (st Store) list(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	type f struct {
		path string
		ts   int64
	}
	var fs []f
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".snap.gz") {
			continue
		}
		ts, err := strconv.ParseInt(strings.TrimSuffix(n, ".snap.gz"), 10, 64)
		if err != nil {
			continue
		}
		fs = append(fs, f{filepath.Join(dir, n), ts})
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].ts < fs[j].ts })
	out := make([]string, len(fs))
	for i, x := range fs {
		out[i] = x.path
	}
	return out, nil
}

// Latest returns the most recent snapshot of id in mode, or nil when there
// is none (not an error).
func (st Store) Latest(id, mode string) (*Snapshot, error) {
	files, err := st.list(st.keyDir(id, mode))
	if err != nil || len(files) == 0 {
		return nil, err
	}
	// Skip unreadable/corrupt files and fall back to older ones.
	var lastErr error
	for i := len(files) - 1; i >= 0; i-- {
		s, err := readFile(files[i])
		if err == nil {
			return s, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func readFile(path string) (*Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return read(f)
}

func write(w io.Writer, s *Snapshot) error {
	zw := gzip.NewWriter(w)
	bw := bufio.NewWriter(zw)
	fmt.Fprintf(bw, "%s\t%d\t%s\t%d\t%d\t%s\n", magic, s.Time.Unix(), s.Mode, s.Entries, s.Size, strconv.Quote(s.ID))
	paths := make([]string, 0, len(s.Sizes))
	for p := range s.Sizes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var line []byte
	for _, p := range paths {
		line = strconv.AppendInt(line[:0], s.Sizes[p], 10)
		line = append(line, '\t')
		line = strconv.AppendQuote(line, p)
		line = append(line, '\n')
		bw.Write(line)
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

func read(r io.Reader) (*Snapshot, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	if !sc.Scan() {
		return nil, fmt.Errorf("instantané vide")
	}
	h := strings.SplitN(sc.Text(), "\t", 6)
	if len(h) != 6 || h[0] != magic {
		return nil, fmt.Errorf("format d'instantané inconnu")
	}
	ts, err1 := strconv.ParseInt(h[1], 10, 64)
	entries, err2 := strconv.ParseInt(h[3], 10, 64)
	size, err3 := strconv.ParseInt(h[4], 10, 64)
	id, err4 := strconv.Unquote(h[5])
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return nil, fmt.Errorf("en-tête d'instantané invalide : %w", err)
	}
	s := &Snapshot{ID: id, Mode: h[2], Time: time.Unix(ts, 0), Entries: entries, Size: size, Sizes: make(map[string]int64)}
	for sc.Scan() {
		line := sc.Text()
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("ligne d'instantané invalide")
		}
		sz, err := strconv.ParseInt(line[:tab], 10, 64)
		if err != nil {
			return nil, err
		}
		p, err := strconv.Unquote(line[tab+1:])
		if err != nil {
			return nil, err
		}
		s.Sizes[p] = sz
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return s, nil
}
