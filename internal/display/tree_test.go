package display

import (
	"bytes"
	"strings"
	"testing"

	"github.com/D1nma/disk_check/internal/scanner"
)

// buildTestTree returns:
//
//	root/           own 4096, size 4096 + 16384 + inner(12288) = 32768
//	├── file.bin    16384 blocks
//	└── inner/      own 4096, size 4096 + 8192 = 12288
//	    └── data    8192 blocks
func buildTestTree() *scanner.Node {
	inner := &scanner.Node{Name: "inner", IsDir: true, OwnSize: 4096, Size: 12288, DirCount: 0, FileCount: 1}
	data := &scanner.Node{Name: "data", Size: 8192, FileCount: 1}
	inner.Children = []*scanner.Node{data}
	data.Parent = inner

	root := &scanner.Node{Name: "/tmp/x", IsDir: true, OwnSize: 4096, Size: 32768, DirCount: 1, FileCount: 2}
	file := &scanner.Node{Name: "file.bin", Size: 16384, FileCount: 1}
	// deliberately smallest-first so the display sort must reorder
	root.Children = []*scanner.Node{inner, file}
	file.Parent = root
	inner.Parent = root
	return root
}

func TestTreeDepthAndSizes(t *testing.T) {
	var buf bytes.Buffer
	Tree(&buf, buildTestTree(), 1)

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	// header, blank, root, then exactly 2 depth-1 lines (depth cutoff)
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines at depth 1, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[2], " 28.0 KiB  100.0%  ./") {
		t.Errorf("root line wrong: %q (want displayed 28.0 KiB, own blocks excluded)", lines[2])
	}
	// children sorted biggest-first
	if !strings.Contains(lines[3], "file.bin") {
		t.Errorf("expected file.bin (16 KiB) before inner (8 KiB), line: %q", lines[3])
	}
	if !strings.Contains(lines[4], "inner") {
		t.Errorf("missing inner line: %q", lines[4])
	}
	if !strings.Contains(lines[4], "  8.0 KiB") {
		t.Errorf("inner displayed size must exclude its own 4 KiB block: %q", lines[4])
	}
}

func TestTreeDepthCutoffDeeper(t *testing.T) {
	var buf bytes.Buffer
	Tree(&buf, buildTestTree(), 2)

	if !strings.Contains(buf.String(), "data") {
		t.Error("depth 2 must show the second level (data)")
	}
	var buf0 bytes.Buffer
	Tree(&buf0, buildTestTree(), 0)
	if strings.Contains(buf0.String(), "├──") {
		t.Error("depth 0 must not show children")
	}
}

func TestTreeKeepsChildOrder(t *testing.T) {
	root := buildTestTree()
	var buf bytes.Buffer
	Tree(&buf, root, 1)
	// the shared tree order (inner first) must survive display sorting
	if root.Children[0].Name != "inner" || root.Children[1].Name != "file.bin" {
		t.Errorf("display sort must not reorder the shared tree, children now: %s, %s",
			root.Children[0].Name, root.Children[1].Name)
	}
	if !strings.Contains(buf.String(), "file.bin") {
		t.Error("sanity: output should contain file.bin")
	}
}
