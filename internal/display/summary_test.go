package display

import (
	"bytes"
	"strings"
	"testing"

	"github.com/D1nma/disk_check/internal/scanner"
)

func TestSummaryContainsLocalContext(t *testing.T) {
	root := &scanner.Node{Name: "/tmp/x", Size: 4096, FileCount: 2, DirCount: 1, IsDir: true}
	dir := &scanner.Node{Name: "sub", Size: 2048, IsDir: true, Parent: root}
	file := &scanner.Node{Name: "f.bin", Size: 1024, Parent: root}
	root.Children = []*scanner.Node{dir, file}

	var buf bytes.Buffer
	Summary(&buf, root.Name, root, root.Children, []*scanner.Node{file}, DiskInfo{Total: 10000, Used: 5000, Avail: 5000}, 20)
	s := buf.String()
	for _, want := range []string{"Dossier : /tmp/x", "Taille", "fichiers", "TOP SOUS-DOSSIERS", "TOP FICHIERS", "f.bin"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}
