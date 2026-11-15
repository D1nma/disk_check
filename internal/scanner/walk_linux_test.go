//go:build linux

package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestListDirHugeDirFallback(t *testing.T) {
	tmp := t.TempDir()
	savedMax := entBufMax
	entBufMax = 4
	defer func() { entBufMax = savedMax }()

	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(tmp, fmt.Sprintf("f%02d", i)), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	lc := &listCtx{}
	ents, _, err := listDir(lc, tmp, true, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(ents))
	}
	seen := make(map[string]bool)
	for _, e := range ents {
		if seen[e.name] {
			t.Fatalf("duplicate entry %q", e.name)
		}
		seen[e.name] = true
	}
	if cap(lc.entries) > 4 {
		t.Errorf("reusable buffer grew to %d, want capped at 4", cap(lc.entries))
	}
}
