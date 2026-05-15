package scanner

import (
	"context"
	"os"
	"path/filepath"
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
			if curr.Path == path {
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

	if nDir2.Size != nFile3.Size {
		t.Errorf("dir2 size mismatch: expected %d, got %d", nFile3.Size, nDir2.Size)
	}
	if nDir1.Size != nFile2.Size+nDir2.Size {
		t.Errorf("dir1 size mismatch: expected %d, got %d", nFile2.Size+nDir2.Size, nDir1.Size)
	}
	if root.Size != nFile1.Size+nDir1.Size {
		t.Errorf("root size mismatch: expected %d, got %d", nFile1.Size+nDir1.Size, root.Size)
	}

	if root.FileCount != 3 {
		t.Errorf("expected 3 files, got %d", root.FileCount)
	}
	if root.DirCount != 2 {
		t.Errorf("expected 2 dirs (dir1, dir2), got %d", root.DirCount)
	}
}

func TestScanTopFiles(t *testing.T) {
	tmp, err := os.MkdirTemp("", "scanner_topfiles_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	// Create files of different sizes
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

	ctx := context.Background()
	top := ScanTopFiles(ctx, tmp, 2, ScanOptions{})

	if len(top) != 2 {
		t.Fatalf("expected 2 top files, got %d", len(top))
	}

	if filepath.Base(top[0].Path) != "huge" {
		t.Errorf("expected largest file to be huge, got %s", filepath.Base(top[0].Path))
	}
	if filepath.Base(top[1].Path) != "large" {
		t.Errorf("expected second largest file to be large, got %s", filepath.Base(top[1].Path))
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
		if filepath.Base(child.Path) == "dir1" {
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
