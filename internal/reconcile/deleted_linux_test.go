//go:build linux

package reconcile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// heldDeleted creates name in dir with size bytes, keeps it open for the
// rest of the test and unlinks it. It then creates "name (deleted)" in dir
// as a symlink to this process's /proc/self/fd entry: a fake /proc/PID/fd
// link pointing at that path reads exactly like a real one ("... (deleted)")
// and stat-ing it reaches the unlinked inode (nlink 0), as through the
// kernel's magic link.
func heldDeleted(t *testing.T, dir, name string, size int) (path string, st unix.Stat_t) {
	t.Helper()
	path = filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if _, err := f.Write(bytes.Repeat([]byte("x"), size)); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()), path+deletedSuffix); err != nil {
		t.Fatal(err)
	}
	return path, st
}

func requireProcFD(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		t.Skip("/proc/self/fd unavailable:", err)
	}
}

func mkLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeletedOpenFakeProc(t *testing.T) {
	requireProcFD(t)
	files, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proc := t.TempDir()

	appLog, app := heldDeleted(t, files, "app.log", 64<<10)
	deleted := appLog + deletedSuffix

	// Still linked although its name says otherwise: not freed on close.
	kept := filepath.Join(files, "kept.log"+deletedSuffix)
	writeFile(t, kept, "still here")
	live := filepath.Join(files, "normal.log")
	writeFile(t, live, "live")

	// A deleted directory (e.g. a cwd) is not a regular file.
	gone := filepath.Join(files, "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := os.Open(gone)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	mkLink(t, fmt.Sprintf("/proc/self/fd/%d", d.Fd()), gone+deletedSuffix)

	writeFile(t, filepath.Join(proc, "123", "comm"), "java\n")
	fd := filepath.Join(proc, "123", "fd")
	mkLink(t, "/dev/null", filepath.Join(fd, "0"))
	mkLink(t, live, filepath.Join(fd, "1"))
	mkLink(t, "socket:[12345]", filepath.Join(fd, "3"))
	mkLink(t, deleted, filepath.Join(fd, "4"))
	mkLink(t, kept, filepath.Join(fd, "5"))
	mkLink(t, "/memfd:buf (deleted)", filepath.Join(fd, "6"))
	mkLink(t, "pipe:[999]", filepath.Join(fd, "7"))
	mkLink(t, "anon_inode:[eventfd]", filepath.Join(fd, "8"))
	mkLink(t, gone+deletedSuffix, filepath.Join(fd, "9"))
	mkLink(t, deleted, filepath.Join(fd, "11")) // same inode, second fd
	mkLink(t, deleted, filepath.Join(fd, "not-a-fd"))

	// Lower PID, but sorts after "123" as a string.
	writeFile(t, filepath.Join(proc, "45", "comm"), "nginx\n")
	mkLink(t, deleted, filepath.Join(proc, "45", "fd", "8"))
	mkLink(t, deleted, filepath.Join(proc, "45", "fd", "12"))

	// Noise: a vanished process, non-PID entries.
	if err := os.MkdirAll(filepath.Join(proc, "999"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkLink(t, "123", filepath.Join(proc, "self"))
	writeFile(t, filepath.Join(proc, "meminfo"), "MemTotal: 1 kB\n")
	if err := os.MkdirAll(filepath.Join(proc, "sys", "fd"), 0o755); err != nil {
		t.Fatal(err)
	}

	want := DeletedFile{
		PID: 45, Comm: "nginx", FD: 8, Holders: 4, Path: appLog,
		Size: int64(app.Blocks) * 512, Dev: uint64(app.Dev), Ino: uint64(app.Ino),
	}
	check := func(t *testing.T, got []DeletedFile, hidden, wantHidden int) {
		t.Helper()
		if len(got) != 1 || got[0] != want {
			t.Fatalf("got %+v\nwant [%+v]", got, want)
		}
		if hidden != wantHidden {
			t.Errorf("hidden = %d, want %d", hidden, wantHidden)
		}
	}

	got, hidden, err := DeletedOpen(proc, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want.Size <= 0 {
		t.Fatalf("test file has no allocated blocks: %+v", app)
	}
	check(t, got, hidden, 0)

	t.Run("dev filter", func(t *testing.T) {
		got, hidden, err := DeletedOpen(proc, uint64(app.Dev))
		if err != nil {
			t.Fatal(err)
		}
		check(t, got, hidden, 0)
		got, _, err = DeletedOpen(proc, uint64(app.Dev)+1)
		if err != nil || len(got) != 0 {
			t.Errorf("other device: %+v, %v", got, err)
		}
	})

	t.Run("unreadable process", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses directory permissions")
		}
		locked := filepath.Join(proc, "777")
		mkLink(t, deleted, filepath.Join(locked, "fd", "3"))
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		got, hidden, err := DeletedOpen(proc, 0)
		if err != nil {
			t.Fatal(err)
		}
		check(t, got, hidden, 1)
	})

	t.Run("missing procRoot", func(t *testing.T) {
		if _, _, err := DeletedOpen(filepath.Join(proc, "nope"), 0); err == nil {
			t.Error("want an error for a missing procRoot")
		}
	})
}

func TestDeletedOpenOrder(t *testing.T) {
	requireProcFD(t)
	files, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proc := t.TempDir()
	small, _ := heldDeleted(t, files, "small.log", 8<<10)
	big, _ := heldDeleted(t, files, "big.log", 256<<10)
	writeFile(t, filepath.Join(proc, "10", "comm"), "a\n")
	mkLink(t, small+deletedSuffix, filepath.Join(proc, "10", "fd", "3"))
	mkLink(t, big+deletedSuffix, filepath.Join(proc, "10", "fd", "4"))

	got, _, err := DeletedOpen(proc, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != big || got[1].Path != small || got[0].Size < got[1].Size {
		t.Errorf("not sorted by size desc: %+v", got)
	}
	if got[0].Comm != "a" || got[0].Holders != 1 {
		t.Errorf("unexpected entry: %+v", got[0])
	}
}

// TestDeletedOpenRealProc goes through the kernel's own magic links.
func TestDeletedOpenRealProc(t *testing.T) {
	requireProcFD(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "held.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(bytes.Repeat([]byte("y"), 128<<10)); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		t.Fatal(err)
	}

	got, _, err := DeletedOpen("/proc", uint64(st.Dev))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if d.Ino != uint64(st.Ino) {
			continue
		}
		if d.PID != os.Getpid() || d.FD != int(f.Fd()) || d.Path != path ||
			d.Size != int64(st.Blocks)*512 || d.Holders != 1 || d.Comm == "" {
			t.Errorf("entry = %+v; want pid %d fd %d path %q size %d", d, os.Getpid(), f.Fd(), path, int64(st.Blocks)*512)
		}
		return
	}
	t.Fatalf("held deleted file not found among %d entries", len(got))
}

func TestDeletedPath(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"/var/log/app.log (deleted)", "/var/log/app.log", true},
		{"/tmp/x (deleted) (deleted)", "/tmp/x (deleted)", true},
		{"/tmp/#123 (deleted)", "/tmp/#123", true}, // O_TMPFILE
		{"/var/log/app.log", "", false},
		{"/memfd:buf (deleted)", "", false},
		{"socket:[1] (deleted)", "", false},
		{"anon_inode:[eventfd]", "", false},
		{"pipe:[9]", "", false},
	}
	for _, c := range cases {
		got, ok := deletedPath(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("deletedPath(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
