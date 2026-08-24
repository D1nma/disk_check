package scanner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestScan_DepthOne(t *testing.T) {
	tmp, err := os.MkdirTemp("", "scanner_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	// tmp/
	//   dir1/
	//     file2.txt  ("world")
	//   file1.txt    ("hello")
	dir1 := filepath.Join(tmp, "dir1")
	if err := os.Mkdir(dir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "file1.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir1, "file2.txt"), []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var finalProgress ScanProgress
	for p := range Scan(ctx, tmp, ScanOptions{}) {
		if p.Done {
			finalProgress = p
		}
	}

	root := finalProgress.Root
	if root == nil {
		t.Fatal("expected root node in final progress")
	}

	// Depth-1: only dir1 and file1.txt
	if len(root.Children) != 2 {
		t.Fatalf("expected 2 depth-1 children, got %d", len(root.Children))
	}

	byName := make(map[string]*Node)
	for _, child := range root.Children {
		byName[child.Name] = child
	}

	if _, ok := byName["dir1"]; !ok {
		t.Error("dir1 not found in results")
	}
	if n, ok := byName["dir1"]; ok && !n.IsDir {
		t.Error("dir1 should be IsDir=true")
	}
	// dir1 cumulative size must include file2.txt (> 0 bytes)
	if n, ok := byName["dir1"]; ok && n.Size <= 0 {
		t.Errorf("dir1 cumulative size should be > 0, got %d", n.Size)
	}
	if _, ok := byName["file1.txt"]; !ok {
		t.Error("file1.txt not found in results")
	}
	if n, ok := byName["file1.txt"]; ok && n.IsDir {
		t.Error("file1.txt should be IsDir=false")
	}

	// file2.txt must NOT appear at the top level
	if _, ok := byName["file2.txt"]; ok {
		t.Error("file2.txt must not appear as a depth-1 entry (it is nested)")
	}
}

func TestScan_ContextCancellation(t *testing.T) {
	tmp, err := os.MkdirTemp("", "scanner_cancel_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	for i := 0; i < 8; i++ {
		dir := filepath.Join(tmp, string(rune('a'+i)))
		os.MkdirAll(dir, 0755)
		for j := 0; j < 10; j++ {
			os.WriteFile(filepath.Join(dir, string(rune('a'+j))), []byte("x"), 0644)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before scan starts

	count := 0
	for range Scan(ctx, tmp, ScanOptions{}) {
		count++
	}
	// With context cancelled, it should finish quickly with few or no progress reports.
	if count >= 80 { // 8 * 10 files
		t.Errorf("expected scan to be cancelled, got %d progress reports", count)
	}
}

func TestSizeAggregation(t *testing.T) {
	tmp, err := os.MkdirTemp("", "scanner_size_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	// tmp/ (root)
	//   file1 (100 bytes)
	//   dir1/
	//     file2 (200 bytes)
	//     dir2/
	//       file3 (300 bytes)

	if err := os.WriteFile(filepath.Join(tmp, "file1"), make([]byte, 100), 0644); err != nil {
		t.Fatal(err)
	}
	dir1 := filepath.Join(tmp, "dir1")
	if err := os.Mkdir(dir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir1, "file2"), make([]byte, 200), 0644); err != nil {
		t.Fatal(err)
	}
	dir2 := filepath.Join(dir1, "dir2")
	if err := os.Mkdir(dir2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "file3"), make([]byte, 300), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var root *Node
	for p := range Scan(ctx, tmp, ScanOptions{}) {
		if p.Done {
			root = p.Root
		}
	}

	if root == nil {
		t.Fatal("expected root node")
	}

	// We use blockSize, which might be different from raw size if filesystem uses blocks.
	// But in tests, we can at least check relative sizes or exact if blockSize is identity (which it is for non-syscall).
	// On Linux, it will likely use blocks. Let's find the nodes.

	findNode := func(n *Node, path string) *Node {
		var walk func(*Node) *Node
		walk = func(curr *Node) *Node {
			if curr.Path() == path {
				return curr
			}
			for _, child := range curr.Children {
				if res := walk(child); res != nil {
					return res
				}
			}
			return nil
		}
		return walk(n)
	}

	nDir1 := findNode(root, dir1)
	nDir2 := findNode(root, dir2)
	nFile1 := findNode(root, filepath.Join(tmp, "file1"))
	nFile2 := findNode(root, filepath.Join(dir1, "file2"))
	nFile3 := findNode(root, filepath.Join(dir2, "file3"))

	if nDir2.Size < nFile3.Size {
		t.Errorf("dir2 size %d < file3 %d", nDir2.Size, nFile3.Size)
	}
	if nDir1.Size < nFile2.Size+nDir2.Size {
		t.Errorf("dir1 size %d < children %d", nDir1.Size, nFile2.Size+nDir2.Size)
	}
	if root.Size < nFile1.Size+nDir1.Size {
		t.Errorf("root size %d < children %d", root.Size, nFile1.Size+nDir1.Size)
	}

	if root.FileCount != 3 {
		t.Errorf("expected 3 files, got %d", root.FileCount)
	}
	if root.DirCount != 2 {
		t.Errorf("expected 2 dirs (dir1, dir2), got %d", root.DirCount)
	}
}

func TestTopFiles(t *testing.T) {
	tmp := t.TempDir()
	files := []struct {
		name string
		size int
	}{
		{"small", 10},
		{"medium", 100},
		{"large", 1000},
		{"huge", 10000},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmp, f.name), make([]byte, f.size), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var root *Node
	for p := range Scan(context.Background(), tmp, ScanOptions{}) {
		if p.Done {
			root = p.Root
		}
	}
	if root == nil {
		t.Fatal("no root")
	}
	top := TopFiles(root, 2)
	if len(top) != 2 {
		t.Fatalf("expected 2, got %d", len(top))
	}
	if top[0].Name != "huge" {
		t.Errorf("expected huge, got %s", top[0].Name)
	}
	if top[1].Name != "large" {
		t.Errorf("expected large, got %s", top[1].Name)
	}
}

func TestScanTree(t *testing.T) {
	tmp, err := os.MkdirTemp("", "scanner_tree_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	// tmp/
	//   file1 (100)
	//   dir1/
	//     file2 (200)

	if err := os.WriteFile(filepath.Join(tmp, "file1"), make([]byte, 100), 0644); err != nil {
		t.Fatal(err)
	}
	dir1 := filepath.Join(tmp, "dir1")
	if err := os.Mkdir(dir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir1, "file2"), make([]byte, 200), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	tree, err := ScanTree(ctx, tmp, 1, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if tree == nil {
		t.Fatal("expected tree")
	}

	// Depth 1: should have file1 and dir1
	if len(tree.Children) != 2 {
		t.Fatalf("expected 2 children at depth 1, got %d", len(tree.Children))
	}

	// dir1 should have children empty because maxDepth=1
	for _, child := range tree.Children {
		if child.Name == "dir1" {
			if len(child.Children) != 0 {
				t.Errorf("expected dir1 to have 0 children due to maxDepth=1, got %d", len(child.Children))
			}
			// But it should have its size calculated by sumDir
			if child.Size < 200 {
				t.Errorf("expected dir1 size >= 200, got %d", child.Size)
			}
		}
	}
}

func TestNodePath(t *testing.T) {
	root := &Node{Name: "/home/user", IsDir: true}
	child := &Node{Name: "docs", IsDir: true, Parent: root}
	file := &Node{Name: "a.txt", Parent: child}
	root.Children = []*Node{child}
	child.Children = []*Node{file}

	if got := root.Path(); got != "/home/user" {
		t.Errorf("root.Path()=%q", got)
	}
	if got := child.Path(); got != "/home/user/docs" {
		t.Errorf("child.Path()=%q", got)
	}
	if got := file.Path(); got != "/home/user/docs/a.txt" {
		t.Errorf("file.Path()=%q", got)
	}

	slash := &Node{Name: "/", IsDir: true}
	etc := &Node{Name: "etc", IsDir: true, Parent: slash}
	if got := etc.Path(); got != "/etc" {
		t.Errorf("slash join Path()=%q, want /etc", got)
	}
}

func TestScanExclude(t *testing.T) {
	tmp := t.TempDir()
	keep := filepath.Join(tmp, "keep")
	skip := filepath.Join(tmp, "skip")
	if err := os.Mkdir(keep, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(skip, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keep, "a"), []byte("aa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skip, "b"), []byte("bbbbbbbb"), 0644); err != nil {
		t.Fatal(err)
	}

	var root *Node
	for p := range Scan(context.Background(), tmp, ScanOptions{Excludes: []string{skip}}) {
		if p.Done {
			root = p.Root
		}
	}
	if root == nil {
		t.Fatal("no root")
	}
	for _, c := range root.Children {
		if c.Name == "skip" {
			t.Fatal("excluded dir still present")
		}
	}
}

func TestIsKernFS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("kernfs skip is Linux-only")
	}
	if !isKernFS(0x9fa0) {
		t.Error("proc should be kernfs")
	}
	if !isKernFS(0x62656572) {
		t.Error("sysfs should be kernfs")
	}
	if isKernFS(0x9123683e) {
		t.Error("btrfs must not be skipped")
	}
	if isKernFS(0x01021994) {
		t.Error("tmpfs must not be skipped (would drop /tmp)")
	}
}

func TestScanHardlinkCountedOnce(t *testing.T) {
	tmp := t.TempDir()
	a := filepath.Join(tmp, "a")
	b := filepath.Join(tmp, "b")
	if err := os.WriteFile(a, make([]byte, 4096), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}

	var root *Node
	for p := range Scan(context.Background(), tmp, ScanOptions{}) {
		if p.Done {
			root = p.Root
		}
	}
	if root == nil {
		t.Fatal("no root")
	}
	var na, nb *Node
	for _, c := range root.Children {
		switch c.Name {
		case "a":
			na = c
		case "b":
			nb = c
		}
	}
	if na == nil || nb == nil {
		t.Fatalf("missing names: a=%v b=%v", na, nb)
	}
	if na.Size == 0 && nb.Size == 0 {
		t.Fatal("both hardlink sizes are 0")
	}
	if na.Size > 0 && nb.Size > 0 {
		t.Fatalf("hardlink counted twice: %d and %d", na.Size, nb.Size)
	}
}

func TestWorkerCountBounds(t *testing.T) {
	n := workerCount()
	if n < 2 || n > 16 {
		t.Fatalf("workerCount=%d want in [2,16]", n)
	}
}

func TestListDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(tmp, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	ents, dirSize, _, err := listDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if dirSize < 0 {
		t.Fatalf("dirSize=%d", dirSize)
	}
	var sawFile, sawDir bool
	for _, e := range ents {
		switch e.name {
		case "file.txt":
			sawFile = true
			if e.isDir {
				t.Error("file.txt should not be a dir")
			}
			if e.size <= 0 {
				t.Errorf("file.txt size=%d", e.size)
			}
		case "sub":
			sawDir = true
			if !e.isDir {
				t.Error("sub should be a dir")
			}
		}
	}
	if !sawFile || !sawDir {
		t.Fatalf("missing entries: file=%v dir=%v (n=%d)", sawFile, sawDir, len(ents))
	}
}
