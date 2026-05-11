package scanner

import (
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

	results := Scan(tmpDir, 2)

	var allEntries []Entry
	for res := range results {
		if res.Error != nil {
			t.Errorf("Scan error: %v", res.Error)
			continue
		}
		allEntries = append(allEntries, res.Entries...)
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

func TestScanNonExistent(t *testing.T) {
	results := Scan("/non/existent/path/for/sure", 2)
	
	var errFound bool
	for res := range results {
		if res.Error != nil {
			errFound = true
		}
	}
	
	if !errFound {
		t.Errorf("Expected error for non-existent path, got none")
	}
}
