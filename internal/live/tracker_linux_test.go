//go:build linux

package live

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fakeProc is a /proc look-alike in a temp dir: PID/io, PID/stat and
// PID/fd/N symlinks, plus a few non-process entries Poll must skip.
type fakeProc struct {
	t    *testing.T
	root string
}

func newFakeProc(t *testing.T) *fakeProc {
	t.Helper()
	root := t.TempDir()
	must(t, os.WriteFile(filepath.Join(root, "meminfo"), []byte("MemTotal: 1 kB\n"), 0o644))
	must(t, os.Mkdir(filepath.Join(root, "sys"), 0o755))
	must(t, os.Symlink("1", filepath.Join(root, "self")))
	return &fakeProc{t: t, root: root}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// proc creates or updates process pid.
func (f *fakeProc) proc(pid int, comm string, start, writeBytes uint64) {
	f.t.Helper()
	dir := filepath.Join(f.root, strconv.Itoa(pid))
	must(f.t, os.MkdirAll(filepath.Join(dir, "fd"), 0o755))
	stat := fmt.Sprintf("%d (%s) S 1 %d %d 0 -1 4194560 10 0 0 0 7 3 0 0 20 0 1 0 %d 123456 789 18446744073709551615 1 1 0 0 0 0 0 4096 0 0 0 0 17 2 0 0 0 0 0\n",
		pid, comm, pid, pid, start)
	must(f.t, os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644))
	// cancelled_write_bytes contains "write_bytes:" and must not be picked.
	io := fmt.Sprintf("rchar: 1\nwchar: %d\nsyscr: 1\nsyscw: 1\nread_bytes: 0\nwrite_bytes: %d\ncancelled_write_bytes: 999999999999\n",
		writeBytes*3, writeBytes)
	must(f.t, os.WriteFile(filepath.Join(dir, "io"), []byte(io), 0o644))
	must(f.t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))
}

// fd points PID/fd/n at target.
func (f *fakeProc) fd(pid, n int, target string) {
	f.t.Helper()
	link := filepath.Join(f.root, strconv.Itoa(pid), "fd", strconv.Itoa(n))
	_ = os.Remove(link)
	must(f.t, os.Symlink(target, link))
}

func (f *fakeProc) remove(pid int) {
	f.t.Helper()
	must(f.t, os.RemoveAll(filepath.Join(f.root, strconv.Itoa(pid))))
}

func devOf(t *testing.T, path string) uint64 {
	t.Helper()
	var st unix.Stat_t
	must(t, unix.Stat(path, &st))
	return uint64(st.Dev)
}

func allocated(t *testing.T, path string) int64 {
	t.Helper()
	var st unix.Stat_t
	must(t, unix.Stat(path, &st))
	return int64(st.Blocks) * 512
}

// newFile creates path with size bytes of data.
func newFile(t *testing.T, path string, size int) string {
	t.Helper()
	must(t, os.WriteFile(path, bytes.Repeat([]byte{'x'}, size), 0o644))
	return path
}

// grow appends n bytes to the open file w (or path when w is nil) and
// returns how much its allocation grew. It skips the test on filesystems
// that update st_blocks lazily (ZFS, some network filesystems).
func grow(t *testing.T, path string, w *os.File, n int) int64 {
	t.Helper()
	before := allocated(t, path)
	f := w
	if f == nil {
		var err error
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		must(t, err)
		defer f.Close()
	}
	_, err := f.Write(bytes.Repeat([]byte{'y'}, n))
	must(t, err)
	d := allocated(t, path) - before
	if d <= 0 {
		_ = f.Sync()
		d = allocated(t, path) - before
	}
	if d <= 0 {
		t.Skipf("st_blocks of %s did not grow after a %d-byte write: filesystem allocates lazily", path, n)
	}
	return d
}

func poll(t *testing.T, tr *Tracker, at time.Time) Status {
	t.Helper()
	st, err := tr.Poll(at)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return st
}

func TestPollWritersAndFiles(t *testing.T) {
	fp := newFakeProc(t)
	dir := t.TempDir()
	app := newFile(t, filepath.Join(dir, "app.log"), 64<<10)
	other := newFile(t, filepath.Join(dir, "other.log"), 64<<10)

	fp.proc(100, "java", 1000, 1_000_000)
	fp.fd(100, 0, "/dev/null")      // character device
	fp.fd(100, 3, app)              // growing log
	fp.fd(100, 4, other)            // static file
	fp.fd(100, 5, "socket:[12345]") // dangling, not a path
	fp.fd(100, 6, "pipe:[999]")
	fp.fd(100, 7, dir) // directory
	fp.proc(200, "rsys )log (d)", 2000, 5_000_000)
	fp.proc(300, "idle", 3000, 42)

	tr := NewTracker(fp.root, devOf(t, dir))
	st := poll(t, tr, t0)
	if !st.Warmup || st.Writers != nil || st.Files != nil || st.Hidden != 0 {
		t.Fatalf("first poll: %+v, want warmup only", st)
	}

	fp.proc(100, "java", 1000, 1_000_000+40*mib)         // 20 MiB/s over 2 s
	fp.proc(200, "rsys )log (d)", 2000, 5_000_000+4*mib) // 2 MiB/s
	st = poll(t, tr, t0.Add(2*time.Second))
	if st.Warmup {
		t.Error("second poll still Warmup")
	}
	want := []Writer{{100, "java", 20 * mib}, {200, "rsys )log (d)", 2 * mib}}
	if fmt.Sprint(st.Writers) != fmt.Sprint(want) {
		t.Errorf("Writers = %v, want %v", st.Writers, want)
	}
	if len(st.Files) != 0 {
		t.Errorf("Files = %v on first fd scan, want none (no baseline)", st.Files)
	}

	d := grow(t, app, nil, 256<<10)
	fp.proc(100, "java", 1000, 1_000_000+80*mib)
	st = poll(t, tr, t0.Add(4*time.Second))
	wantFile := GrowingFile{Path: app, Size: allocated(t, app), GrowthBps: float64(d) / 2, PID: 100, Comm: "java", FD: 3}
	if len(st.Files) != 1 || st.Files[0] != wantFile {
		t.Errorf("Files = %+v, want [%+v]", st.Files, wantFile)
	}
	if len(st.Writers) != 1 || st.Writers[0].PID != 100 {
		t.Errorf("Writers = %v, want only pid 100 (200 went idle)", st.Writers)
	}
}

func TestPollDevFilter(t *testing.T) {
	dir := t.TempDir()
	real := devOf(t, dir)
	for _, tc := range []struct {
		name string
		dev  uint64
		want int
	}{
		{"same device", real, 1},
		{"all devices", 0, 1},
		{"other device", real ^ 0x5a5a5a, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakeProc(t)
			app := newFile(t, filepath.Join(dir, tc.name+".log"), 8<<10)
			fp.proc(100, "app", 1, 0)
			fp.fd(100, 3, app)
			tr := NewTracker(fp.root, tc.dev)
			poll(t, tr, t0)
			fp.proc(100, "app", 1, mib)
			poll(t, tr, t0.Add(2*time.Second))
			grow(t, app, nil, 128<<10)
			fp.proc(100, "app", 1, 2*mib)
			st := poll(t, tr, t0.Add(4*time.Second))
			if len(st.Files) != tc.want {
				t.Errorf("Files = %+v, want %d", st.Files, tc.want)
			}
			if len(st.Writers) != 1 {
				t.Errorf("Writers = %v: the device filter applies to files only", st.Writers)
			}
		})
	}
}

func TestPollDeletedFile(t *testing.T) {
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		t.Skip("needs /proc/self/fd")
	}
	fp := newFakeProc(t)
	dir := t.TempDir()

	// A deleted-but-open file: unlink it, keep the descriptor, and make the
	// fake fd link resolve to it through /proc/self/fd while its readlink
	// target carries the kernel's " (deleted)" suffix.
	gone, err := os.Create(filepath.Join(dir, "gone.log"))
	must(t, err)
	defer gone.Close()
	_, err = gone.Write(make([]byte, 8<<10))
	must(t, err)
	must(t, os.Remove(gone.Name()))
	via := filepath.Join(dir, "gone.log (deleted)")
	must(t, os.Symlink("/proc/self/fd/"+strconv.Itoa(int(gone.Fd())), via))

	// A live file whose real name ends in " (deleted)" keeps it.
	weird := newFile(t, filepath.Join(dir, "weird (deleted)"), 8<<10)

	fp.proc(100, "app", 1, 0)
	fp.fd(100, 3, via)
	fp.fd(100, 4, weird)
	tr := NewTracker(fp.root, devOf(t, dir))
	poll(t, tr, t0)
	fp.proc(100, "app", 1, mib)
	poll(t, tr, t0.Add(2*time.Second))
	grow(t, via, gone, 128<<10)
	grow(t, weird, nil, 64<<10)
	fp.proc(100, "app", 1, 2*mib)
	st := poll(t, tr, t0.Add(4*time.Second))

	if len(st.Files) != 2 {
		t.Fatalf("Files = %+v, want 2", st.Files)
	}
	byFD := map[int]GrowingFile{}
	for _, f := range st.Files {
		byFD[f.FD] = f
	}
	if f := byFD[3]; f.Path != filepath.Join(dir, "gone.log") || !f.Deleted {
		t.Errorf("deleted file reported as %+v, want path without suffix and Deleted", f)
	}
	if f := byFD[4]; f.Path != weird || f.Deleted {
		t.Errorf("live file reported as %+v, want its real name, not Deleted", f)
	}
}

func TestPollPIDReuse(t *testing.T) {
	fp := newFakeProc(t)
	fp.proc(100, "old", 1000, 1<<30)
	tr := NewTracker(fp.root, 0)
	poll(t, tr, t0)
	fp.proc(100, "old", 1000, 1<<30+10*mib)
	if st := poll(t, tr, t0.Add(2*time.Second)); len(st.Writers) != 1 || st.Writers[0].WriteBps != 5*mib {
		t.Fatalf("Writers = %v, want old at 5 MiB/s", st.Writers)
	}

	// The pid is reused by a process with a bigger counter: keyed by start
	// time, it must not show (3 GiB - 1 GiB) / 2 s.
	fp.proc(100, "new", 9000, 3<<30)
	if st := poll(t, tr, t0.Add(4*time.Second)); len(st.Writers) != 0 {
		t.Fatalf("Writers = %v after pid reuse, want none (no baseline)", st.Writers)
	}
	fp.proc(100, "new", 9000, 3<<30+4*mib)
	st := poll(t, tr, t0.Add(6*time.Second))
	if len(st.Writers) != 1 || st.Writers[0].Comm != "new" || st.Writers[0].WriteBps != 2*mib {
		t.Fatalf("Writers = %v, want new at 2 MiB/s", st.Writers)
	}

	// A counter going backwards (reused pid, same start tick) resets too.
	fp.proc(100, "new", 9000, 1)
	if st := poll(t, tr, t0.Add(8*time.Second)); len(st.Writers) != 0 {
		t.Fatalf("Writers = %v after counter reset, want none", st.Writers)
	}
	fp.proc(100, "new", 9000, 1+2*mib)
	if st := poll(t, tr, t0.Add(10*time.Second)); len(st.Writers) != 1 || st.Writers[0].WriteBps != mib {
		t.Fatalf("Writers = %v, want new at 1 MiB/s", st.Writers)
	}
}

func TestPollVanishedPID(t *testing.T) {
	fp := newFakeProc(t)
	fp.proc(100, "a", 1, 0)
	fp.proc(200, "b", 2, 0)
	tr := NewTracker(fp.root, 0)
	poll(t, tr, t0)
	fp.proc(100, "a", 1, mib)
	fp.proc(200, "b", 2, mib)
	if st := poll(t, tr, t0.Add(time.Second)); len(st.Writers) != 2 {
		t.Fatalf("Writers = %v, want 2", st.Writers)
	}

	fp.remove(200)
	fp.proc(100, "a", 1, 2*mib)
	st := poll(t, tr, t0.Add(2*time.Second))
	if len(st.Writers) != 1 || st.Writers[0].PID != 100 {
		t.Fatalf("Writers = %v, want only 100", st.Writers)
	}
	if _, ok := tr.procs[200]; ok || len(tr.procs) != 1 {
		t.Errorf("state for vanished pid kept: %v", tr.procs)
	}

	// Back with the same identity: its old baseline was forgotten.
	fp.proc(200, "b", 2, 10*mib)
	if st := poll(t, tr, t0.Add(3*time.Second)); len(st.Writers) != 0 {
		t.Errorf("Writers = %v, want none (200 has no baseline, 100 idle)", st.Writers)
	}
}

func TestPollTopWritersLimit(t *testing.T) {
	fp := newFakeProc(t)
	dir := t.TempDir()
	files := map[int]string{}
	for pid := 101; pid <= 112; pid++ {
		fp.proc(pid, "w"+strconv.Itoa(pid), 1, 0)
		files[pid] = newFile(t, filepath.Join(dir, strconv.Itoa(pid)+".log"), 4<<10)
		fp.fd(pid, 3, files[pid])
	}
	tr := NewTracker(fp.root, devOf(t, dir))
	poll(t, tr, t0)
	bump := func(round uint64) {
		for pid := 101; pid <= 112; pid++ {
			fp.proc(pid, "w"+strconv.Itoa(pid), 1, round*uint64(pid-100)*mib)
		}
	}
	bump(1)
	poll(t, tr, t0.Add(time.Second))
	for pid := 101; pid <= 112; pid++ {
		grow(t, files[pid], nil, (pid-100)*64<<10)
	}
	bump(2)
	st := poll(t, tr, t0.Add(2*time.Second))

	if len(st.Writers) != MaxWriters {
		t.Fatalf("%d writers, want %d", len(st.Writers), MaxWriters)
	}
	for i, w := range st.Writers {
		if w.PID != 112-i || w.WriteBps != float64(112-i-100)*mib {
			t.Errorf("Writers[%d] = %v, want pid %d", i, w, 112-i)
		}
	}
	if len(st.Files) != MaxWriters {
		t.Fatalf("%d files, want %d (only top writers inspected)", len(st.Files), MaxWriters)
	}
	for i, f := range st.Files {
		if f.PID != 112-i || f.Path != files[112-i] {
			t.Errorf("Files[%d] = %+v, want pid %d", i, f, 112-i)
		}
		if i > 0 && f.GrowthBps > st.Files[i-1].GrowthBps {
			t.Errorf("Files not sorted by growth at %d", i)
		}
	}
}

func TestPollFilesLimitAndDedupe(t *testing.T) {
	fp := newFakeProc(t)
	dir := t.TempDir()
	const n = MaxFiles + 2
	paths := make([]string, n)
	fp.proc(1, "big", 1, 0)
	fp.proc(2, "small", 1, 0)
	for i := range paths {
		paths[i] = newFile(t, filepath.Join(dir, fmt.Sprintf("f%02d", i)), 4<<10)
		fp.fd(1, 3+i, paths[i])
	}
	fp.fd(1, 40, paths[0]) // same file twice in one process
	fp.fd(2, 3, paths[0])  // and in a slower writer
	tr := NewTracker(fp.root, devOf(t, dir))
	poll(t, tr, t0)
	fp.proc(1, "big", 1, 10*mib)
	fp.proc(2, "small", 1, mib)
	poll(t, tr, t0.Add(time.Second))
	for i, p := range paths {
		grow(t, p, nil, (n-i)*32<<10) // f00 grows the most
	}
	fp.proc(1, "big", 1, 20*mib)
	fp.proc(2, "small", 1, 2*mib)
	st := poll(t, tr, t0.Add(2*time.Second))

	if len(st.Files) != MaxFiles {
		t.Fatalf("%d files, want %d", len(st.Files), MaxFiles)
	}
	if f := st.Files[0]; f.Path != paths[0] || f.PID != 1 || (f.FD != 3 && f.FD != 40) {
		t.Errorf("Files[0] = %+v, want f00 attributed to pid 1", f)
	}
	for i, f := range st.Files {
		if i > 0 && f.Path == paths[0] {
			t.Errorf("f00 reported twice: %+v", st.Files)
		}
		if i > 0 && f.GrowthBps > st.Files[i-1].GrowthBps {
			t.Errorf("Files not sorted by growth at %d", i)
		}
	}
}

func TestPollHidden(t *testing.T) {
	fp := newFakeProc(t)
	fp.proc(100, "mine", 1, 0)
	fp.proc(200, "root-owned", 1, 0)
	fp.proc(300, "exiting", 1, 0)
	tr := NewTracker(fp.root, 0)
	tr.readFile = func(path string, buf []byte) ([]byte, error) {
		switch {
		case strings.HasSuffix(path, "/200/io"):
			return nil, &fs.PathError{Op: "open", Path: path, Err: unix.EACCES}
		case strings.HasSuffix(path, "/300/io"):
			return nil, &fs.PathError{Op: "open", Path: path, Err: unix.ESRCH}
		}
		return readSmall(path, buf)
	}
	if st := poll(t, tr, t0); st.Hidden != 1 {
		t.Errorf("Hidden = %d, want 1 (EACCES counts, a vanished process does not)", st.Hidden)
	}
}

func TestPollHiddenFDDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory anyway")
	}
	fp := newFakeProc(t)
	fp.proc(100, "app", 1, 0)
	tr := NewTracker(fp.root, 0)
	poll(t, tr, t0)
	fdDir := filepath.Join(fp.root, "100", "fd")
	must(t, os.Chmod(fdDir, 0))
	defer os.Chmod(fdDir, 0o755)
	fp.proc(100, "app", 1, mib)
	if st := poll(t, tr, t0.Add(time.Second)); st.Hidden != 1 || len(st.Writers) != 1 {
		t.Errorf("Hidden = %d, Writers = %v; want 1 hidden, 1 writer", st.Hidden, st.Writers)
	}
}

func TestPollClockBackwards(t *testing.T) {
	fp := newFakeProc(t)
	fp.proc(100, "a", 1, 0)
	tr := NewTracker(fp.root, 0)
	poll(t, tr, t0)
	fp.proc(100, "a", 1, mib)
	if st := poll(t, tr, t0); !st.Warmup || len(st.Writers) != 0 {
		t.Errorf("poll at the same instant: %+v, want Warmup and no rates", st)
	}
	fp.proc(100, "a", 1, 2*mib)
	if st := poll(t, tr, t0.Add(time.Second)); st.Warmup || len(st.Writers) != 1 || st.Writers[0].WriteBps != mib {
		t.Errorf("next poll: %+v, want 1 MiB/s", st)
	}
}

func TestPollMissingRoot(t *testing.T) {
	tr := NewTracker(filepath.Join(t.TempDir(), "nope"), 0)
	if _, err := tr.Poll(t0); err == nil {
		t.Error("Poll on a missing procRoot: want an error")
	}
}

func TestPollRealProc(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	tr := NewTracker("", devOf(t, t.TempDir()))
	if tr.procRoot != "/proc" {
		t.Fatalf("procRoot = %q", tr.procRoot)
	}
	now := time.Now()
	st := poll(t, tr, now)
	if !st.Warmup {
		t.Error("first poll not Warmup")
	}
	if _, ok := tr.procs[os.Getpid()]; !ok {
		t.Errorf("own pid %d not tracked (Hidden = %d)", os.Getpid(), st.Hidden)
	}
	st = poll(t, tr, now.Add(2*time.Second))
	if st.Warmup {
		t.Error("second poll Warmup")
	}
	for _, w := range st.Writers {
		if w.WriteBps <= 0 {
			t.Errorf("writer with rate %v", w.WriteBps)
		}
	}
}

func TestParseStat(t *testing.T) {
	line := []byte("4242 (we ird) (x)) R 1 2 3 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 987654 1 2 3\n")
	start, comm, ok := parseStat(line)
	if !ok || start != 987654 || string(comm) != "we ird) (x)" {
		t.Errorf("parseStat = %d %q %v", start, comm, ok)
	}
	for _, bad := range []string{"", "1 (x", "1 x) R", "1 (x) R 1 2 3", "1 (x) R 1 2 3 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 abc 1"} {
		if _, _, ok := parseStat([]byte(bad)); ok {
			t.Errorf("parseStat(%q) ok, want failure", bad)
		}
	}

	self, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Skip("no /proc")
	}
	want, _ := os.ReadFile("/proc/self/comm")
	start, comm, ok = parseStat(self)
	if !ok || start == 0 || string(comm) != strings.TrimSuffix(string(want), "\n") {
		t.Errorf("parseStat(/proc/self/stat) = %d %q %v, comm file %q", start, comm, ok, want)
	}
}

func TestParseWriteBytes(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"rchar: 1\nwrite_bytes: 4096\ncancelled_write_bytes: 7\n", 4096, true},
		{"cancelled_write_bytes: 7\nwrite_bytes: 12345", 12345, true},
		{"cancelled_write_bytes: 7\n", 0, false},
		{"write_bytes: -1\n", 0, false},
		{"write_bytes: \n", 0, false},
		{"write_bytes: 18446744073709551615\n", 1<<64 - 1, true},
		{"write_bytes: 18446744073709551616\n", 0, false},
	}
	for _, c := range cases {
		got, ok := parseWriteBytes([]byte(c.in))
		if got != c.want || ok != c.ok {
			t.Errorf("parseWriteBytes(%q) = %d %v, want %d %v", c.in, got, ok, c.want, c.ok)
		}
	}
	if self, err := os.ReadFile("/proc/self/io"); err == nil {
		if _, ok := parseWriteBytes(self); !ok {
			t.Errorf("parseWriteBytes(/proc/self/io) failed on %q", self)
		}
	}
}

func TestParseNum(t *testing.T) {
	for in, want := range map[string]bool{"1": true, "4194304": true, "": false, "self": false, "+1": false, "-1": false, "12a": false} {
		if _, ok := parseNum(in); ok != want {
			t.Errorf("parseNum(%q) ok = %v, want %v", in, ok, want)
		}
	}
}

func TestSplitDeleted(t *testing.T) {
	if p, d := splitDeleted("/var/log/a.log (deleted)", true); p != "/var/log/a.log" || !d {
		t.Errorf("unlinked: %q %v", p, d)
	}
	if p, d := splitDeleted("/var/log/a.log (deleted)", false); p != "/var/log/a.log (deleted)" || d {
		t.Errorf("linked: %q %v", p, d)
	}
	if p, d := splitDeleted("/memfd:x", true); p != "/memfd:x" || !d {
		t.Errorf("unlinked without suffix: %q %v", p, d)
	}
}

func TestReadSmallErrors(t *testing.T) {
	_, err := readSmall(filepath.Join(t.TempDir(), "missing"), make([]byte, 64))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readSmall(missing) = %v, want ErrNotExist", err)
	}
	p := newFile(t, filepath.Join(t.TempDir(), "big"), 100)
	got, err := readSmall(p, make([]byte, 64))
	if err != nil || len(got) != 64 {
		t.Errorf("readSmall into a short buffer = %d bytes, %v; want 64", len(got), err)
	}
}

func BenchmarkPollRealProc(b *testing.B) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		b.Skip("no /proc")
	}
	tr := NewTracker("/proc", 0)
	now := time.Now()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		if _, err := tr.Poll(now.Add(time.Duration(i) * 2 * time.Second)); err != nil {
			b.Fatal(err)
		}
	}
}
