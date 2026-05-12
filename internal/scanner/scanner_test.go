package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScan(t *testing.T) {
	// Create a temporary directory structure for testing
	tmpDir, err := os.MkdirTemp("", "scanner_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create some files and directories
	dir1 := filepath.Join(tmpDir, "dir1")
	if err := os.Mkdir(dir1, 0755); err != nil {
		t.Fatalf("Failed to create dir1: %v", err)
	}
	file1 := filepath.Join(tmpDir, "file1.txt")
	if err := os.WriteFile(file1, []byte("hello"), 0644); err != nil {
		t.Fatalf("Failed to create file1: %v", err)
	}
	file2 := filepath.Join(dir1, "file2.txt")
	if err := os.WriteFile(file2, []byte("world"), 0644); err != nil {
		t.Fatalf("Failed to create file2: %v", err)
	}

	ctx := context.Background()
	results := Scan(ctx, tmpDir, 2)

	var allEntries []Entry
	for entry := range results {
		allEntries = append(allEntries, entry)
	}

	// We expect 3 entries: dir1, file1.txt, and file2.txt
	expectedCount := 3
	if len(allEntries) != expectedCount {
		t.Errorf("Expected %d entries, got %d", expectedCount, len(allEntries))
	}

	// Check if file2.txt is in the results
	found := false
	for _, e := range allEntries {
		if filepath.Base(e.Path) == "file2.txt" {
			found = true
			if e.Size != 5 {
				t.Errorf("Expected size 5 for file2.txt, got %d", e.Size)
			}
			break
		}
	}
	if !found {
		t.Errorf("file2.txt not found in scan results")
	}
}

func TestScanContextCancellation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "scanner_cancel_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a larger structure to ensure we have time to cancel
	for i := 0; i < 10; i++ {
		dir := filepath.Join(tmpDir, "dir"+filepath.Join(string(rune('a'+i))))
		os.MkdirAll(dir, 0755)
		for j := 0; j < 10; j++ {
			file := filepath.Join(dir, "file"+filepath.Join(string(rune('a'+j))))
			os.WriteFile(file, []byte("test"), 0644)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately or very soon
	cancel()

	results := Scan(ctx, tmpDir, 2)

	count := 0
	for range results {
		count++
	}

	// Since we cancelled immediately, we expect very few or zero results
	// The exact number depends on race conditions but it should definitely be less than 110
	if count >= 110 {
		t.Errorf("Expected scan to be cancelled, but got %d entries", count)
	}
}
