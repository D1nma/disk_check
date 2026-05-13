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
	var entries []Entry
	for e := range Scan(ctx, tmp, ScanOptions{}) {
		entries = append(entries, e)
	}

	// Depth-1: only dir1 and file1.txt (file2.txt is nested, not a direct child).
	if len(entries) != 2 {
		t.Fatalf("expected 2 depth-1 entries, got %d: %v", len(entries), entries)
	}

	byName := make(map[string]Entry)
	for _, e := range entries {
		byName[filepath.Base(e.Path)] = e
	}

	if _, ok := byName["dir1"]; !ok {
		t.Error("dir1 not found in results")
	}
	if e, ok := byName["dir1"]; ok && !e.IsDir {
		t.Error("dir1 should be IsDir=true")
	}
	// dir1 cumulative size must include file2.txt (> 0 bytes)
	if e, ok := byName["dir1"]; ok && e.Size <= 0 {
		t.Errorf("dir1 cumulative size should be > 0, got %d", e.Size)
	}
	if _, ok := byName["file1.txt"]; !ok {
		t.Error("file1.txt not found in results")
	}
	if e, ok := byName["file1.txt"]; ok && e.IsDir {
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
	// Should be well under the full 8 entries
	if count >= 8 {
		t.Errorf("expected scan to be mostly cancelled, got %d entries", count)
	}
}
