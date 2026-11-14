package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func finalOf(t *testing.T, root string, opts ScanOptions) ScanProgress {
	t.Helper()
	var last ScanProgress
	got := false
	for p := range Scan(context.Background(), root, opts) {
		if p.Done {
			last = p
			got = true
		}
	}
	if !got {
		t.Fatal("scan ended without a Done message")
	}
	return last
}

func childNamed(n *Node, name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestScanMissingRootReportsError(t *testing.T) {
	p := finalOf(t, filepath.Join(t.TempDir(), "does-not-exist"), ScanOptions{})
	if p.Err == nil {
		t.Fatal("expected Err for a missing root")
	}
	if p.Root != nil {
		t.Fatal("Root must be nil when the root cannot be read")
	}
}

func TestScanUnreadableDirIsFlagged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	tmp := t.TempDir()
	secret := filepath.Join(tmp, "secret")
	if err := os.Mkdir(secret, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secret, "f"), make([]byte, 8192), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(secret, 0755) })

	p := finalOf(t, tmp, ScanOptions{})
	if p.ErrorCount != 1 || len(p.Errors) != 1 || p.Errors[0].Path != secret {
		t.Fatalf("want one error on %s, got count=%d errors=%v", secret, p.ErrorCount, p.Errors)
	}
	s := childNamed(p.Root, "secret")
	if s == nil || s.Flags&FlagUnreadable == 0 {
		t.Fatalf("secret not flagged unreadable: %+v", s)
	}
	if p.Root.Flags&FlagPartial == 0 || !p.Root.Incomplete() {
		t.Fatal("root must be flagged partial")
	}
}

func TestScanHardlinkAttributionIsDeterministic(t *testing.T) {
	tmp := t.TempDir()
	dirs := []string{"h", "c", "a", "f", "b", "e", "d", "g"}
	for _, d := range dirs {
		if err := os.Mkdir(filepath.Join(tmp, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	first := filepath.Join(tmp, "h", "big")
	if err := os.WriteFile(first, make([]byte, 64*1024), 0644); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs[1:] {
		if err := os.Link(first, filepath.Join(tmp, d, "big")); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 15; i++ {
		p := finalOf(t, tmp, ScanOptions{Workers: 8})
		for _, d := range dirs {
			big := childNamed(childNamed(p.Root, d), "big")
			if big == nil || big.Flags&FlagHardlink == 0 {
				t.Fatalf("%s/big missing or not flagged", d)
			}
			if d == "a" && big.Size == 0 {
				t.Fatalf("run %d: the smallest path a/big must carry the size", i)
			}
			if d != "a" && big.Size != 0 {
				t.Fatalf("run %d: %s/big must be 0, got %d", i, d, big.Size)
			}
		}
	}
}

func TestScanFreeableWithHardlinks(t *testing.T) {
	tmp := t.TempDir()
	scanned := filepath.Join(tmp, "scanned")
	for _, d := range []string{"scanned/a", "scanned/b", "scanned/c"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// shared: two links, both inside the scan (a and b).
	shared := filepath.Join(scanned, "a", "shared")
	if err := os.WriteFile(shared, make([]byte, 64*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(shared, filepath.Join(scanned, "b", "shared")); err != nil {
		t.Fatal(err)
	}
	// outside: one link in c, one outside the scanned tree.
	out := filepath.Join(tmp, "outside")
	if err := os.WriteFile(out, make([]byte, 32*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(out, filepath.Join(scanned, "c", "out")); err != nil {
		t.Fatal(err)
	}

	p := finalOf(t, scanned, ScanOptions{})
	a, b, c := childNamed(p.Root, "a"), childNamed(p.Root, "b"), childNamed(p.Root, "c")
	sharedSize := childNamed(a, "shared").Size
	outSize := childNamed(c, "out").Size
	if sharedSize == 0 || outSize == 0 {
		t.Fatalf("sizes not attributed: shared=%d out=%d", sharedSize, outSize)
	}
	if got := Freeable(a, p.Pinned); got != a.Size-sharedSize {
		t.Errorf("Freeable(a)=%d, want %d (b still links the inode)", got, a.Size-sharedSize)
	}
	if got := Freeable(b, p.Pinned); got != b.Size {
		t.Errorf("Freeable(b)=%d, want %d", got, b.Size)
	}
	if got := Freeable(c, p.Pinned); got != c.Size-outSize {
		t.Errorf("Freeable(c)=%d, want %d (link outside the scan)", got, c.Size-outSize)
	}
	// At the root, the shared inode is freed (both links inside), the outside one is not.
	if got, want := Freeable(p.Root, p.Pinned), p.Root.Size-outSize; got != want {
		t.Errorf("Freeable(root)=%d, want %d", got, want)
	}
}

func TestScanDevBytesAndRecent(t *testing.T) {
	tmp := t.TempDir()
	newF := filepath.Join(tmp, "new")
	oldF := filepath.Join(tmp, "sub", "old")
	if err := os.MkdirAll(filepath.Dir(oldF), 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{newF, oldF} {
		if err := os.WriteFile(f, make([]byte, 16*1024), 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(oldF, old, old); err != nil {
		t.Fatal(err)
	}

	p := finalOf(t, tmp, ScanOptions{})
	var sum int64
	for _, v := range p.DevBytes {
		sum += v
	}
	if sum != p.Root.Size {
		t.Errorf("DevBytes sum %d != root size %d", sum, p.Root.Size)
	}
	newSize := childNamed(p.Root, "new").Size
	if p.Recent[p.Root] != newSize {
		t.Errorf("Recent[root]=%d, want %d (only the new file)", p.Recent[p.Root], newSize)
	}
	if _, ok := p.Recent[childNamed(p.Root, "sub")]; ok {
		t.Error("sub has no recent file and must not be in Recent")
	}
}

func TestAggregatePropagatesPartial(t *testing.T) {
	root := &Node{Name: "/r", IsDir: true}
	mid := &Node{Name: "mid", IsDir: true, Parent: root}
	bad := &Node{Name: "bad", IsDir: true, Parent: mid, Flags: FlagUnreadable}
	ok := &Node{Name: "ok", IsDir: true, Parent: root}
	root.Children = []*Node{mid, ok}
	mid.Children = []*Node{bad}
	aggregateTree(root, 1, nil, 0)
	if mid.Flags&FlagPartial == 0 || root.Flags&FlagPartial == 0 {
		t.Fatal("partial flag must propagate to every ancestor")
	}
	if ok.Incomplete() {
		t.Fatal("sibling must stay complete")
	}
}

func TestExpectedEntriesOnlyAtMountPoints(t *testing.T) {
	table := []mountEntry{{path: "/", fsType: "ext4"}}
	if got := expectedEntries("/definitely/not/a/mount", true, table, nil); got != 0 {
		t.Fatalf("non mount point must give 0, got %d", got)
	}
}
