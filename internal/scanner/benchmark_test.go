package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkScanWide measures the complete scan, including tree construction.
func BenchmarkScanWide(b *testing.B) {
	root := b.TempDir()
	for d := 0; d < 100; d++ {
		dir := filepath.Join(root, fmt.Sprintf("dir-%03d", d))
		if err := os.Mkdir(dir, 0755); err != nil {
			b.Fatal(err)
		}
		for f := 0; f < 100; f++ {
			file, err := os.Create(filepath.Join(dir, fmt.Sprintf("file-%03d", f)))
			if err != nil {
				b.Fatal(err)
			}
			file.Close()
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var result ScanProgress
		for p := range Scan(context.Background(), root, ScanOptions{}) {
			if p.Done {
				result = p
			}
		}
		if result.Root == nil || result.Files != 10000 {
			b.Fatalf("unexpected scan: %+v", result)
		}
	}
}

// BenchmarkScanDeep exposes scheduling overhead when little work can run in parallel.
func BenchmarkScanDeep(b *testing.B) {
	root := b.TempDir()
	dir := root
	for d := 0; d < 100; d++ {
		dir = filepath.Join(dir, fmt.Sprintf("dir-%03d", d))
		if err := os.Mkdir(dir, 0755); err != nil {
			b.Fatal(err)
		}
		for f := 0; f < 10; f++ {
			file, err := os.Create(filepath.Join(dir, fmt.Sprintf("file-%02d", f)))
			if err != nil {
				b.Fatal(err)
			}
			file.Close()
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var result ScanProgress
		for p := range Scan(context.Background(), root, ScanOptions{}) {
			if p.Done {
				result = p
			}
		}
		if result.Root == nil || result.Files != 1000 {
			b.Fatalf("unexpected scan: %+v", result)
		}
	}
}
