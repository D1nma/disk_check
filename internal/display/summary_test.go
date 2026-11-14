package display

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/snapshot"
)

func summaryTree() (*scanner.Node, *scanner.Node) {
	root := &scanner.Node{Name: "/tmp/x", Size: 4096, FileCount: 2, DirCount: 1, IsDir: true}
	dir := &scanner.Node{Name: "sub", Size: 2048, IsDir: true, Parent: root}
	file := &scanner.Node{Name: "f.bin", Size: 1024, Parent: root}
	root.Children = []*scanner.Node{file, dir}
	return root, file
}

func TestSummaryContainsLocalContext(t *testing.T) {
	root, file := summaryTree()
	var buf bytes.Buffer
	Summary(&buf, SummaryData{Root: root, TopFiles: []*scanner.Node{file}, Disk: DiskInfo{Total: 10000, Used: 5000, Avail: 5000}, TopN: 20})
	s := buf.String()
	for _, want := range []string{"Dossier : /tmp/x", "Taille", "fichiers", "TOP SOUS-DOSSIERS", "sub/", "TOP FICHIERS", "  f.bin"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "INCOMPLET") || strings.Contains(s, "CROISSANCE") {
		t.Errorf("no warning or growth section expected:\n%s", s)
	}
}

func TestSummaryTopFilesRelativeToRoot(t *testing.T) {
	root, _ := summaryTree()
	deep := &scanner.Node{Name: "deep.log", Size: 512, Parent: root.Children[1]}
	var buf bytes.Buffer
	Summary(&buf, SummaryData{Root: root, TopFiles: []*scanner.Node{deep}, TopN: 5})
	if !strings.Contains(buf.String(), "  sub/deep.log") || strings.Contains(buf.String(), "/tmp/x/sub") {
		t.Errorf("top files must be relative to the root:\n%s", buf.String())
	}
}

func TestSummaryWarnsAndSanitizes(t *testing.T) {
	root, _ := summaryTree()
	root.Children[1].Flags = scanner.FlagUnreadable
	evil := &scanner.Node{Name: "evil\x1b]0;PWNED\x07", Size: 1, Parent: root}
	var buf bytes.Buffer
	Summary(&buf, SummaryData{
		Root: root, TopFiles: []*scanner.Node{evil}, TopN: 5, ErrorCount: 1,
		Errors: []scanner.ScanError{{Path: "/tmp/x/sub", Err: "permission refusée"}},
	})
	s := buf.String()
	if strings.ContainsRune(s, 0x1b) || strings.ContainsRune(s, 0x07) {
		t.Fatalf("control characters leaked: %q", s)
	}
	for _, want := range []string{"SCAN INCOMPLET : 1", "/tmp/x/sub : permission refusée", "sub/  ⚠ illisible"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestSummaryGrowthSection(t *testing.T) {
	root, _ := summaryTree()
	var buf bytes.Buffer
	Summary(&buf, SummaryData{Root: root, TopN: 5, Since: time.Date(2026, 11, 13, 9, 12, 0, 0, time.Local),
		Growth: []snapshot.Growth{{Path: "sub/app.log", Delta: 3 << 30, Exact: true}}})
	if !strings.Contains(buf.String(), "CROISSANCE DEPUIS LE 2026-11-13 09:12") || !strings.Contains(buf.String(), "+3.0 GiB  sub/app.log") {
		t.Errorf("growth section missing:\n%s", buf.String())
	}
}

func TestSummaryJSON(t *testing.T) {
	root, file := summaryTree()
	var buf bytes.Buffer
	if err := SummaryJSON(&buf, SummaryData{Root: root, TopFiles: []*scanner.Node{file}, TopN: 5}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if got["path"] != "/tmp/x" || len(got["top_dirs"].([]any)) != 1 || len(got["top_files"].([]any)) != 1 {
		t.Errorf("unexpected JSON: %s", buf.String())
	}
}
