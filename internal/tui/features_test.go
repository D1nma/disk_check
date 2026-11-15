package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/snapshot"
	tea "github.com/charmbracelet/bubbletea"
)

func browsing(root *scanner.Node) Model {
	m := Model{State: StateBrowsing, Root: root, Current: root, Entries: root.Children, Path: root.Path(), Width: 100, Height: 20, diskTotal: 1 << 40, diskUsed: 1 << 30}
	m.topFiles = make(map[*scanner.Node][]*scanner.Node)
	m.sortEntries()
	return m
}

func lines(v string) int { return strings.Count(v, "\n") + 1 }

func TestFatalScanErrorQuits(t *testing.T) {
	m := Model{State: StateScanning}
	model, cmd := m.Update(progressMsg{progress: scanner.ScanProgress{Done: true, Err: errors.New("/x : introuvable")}})
	if model.(Model).FatalErr == nil {
		t.Fatal("FatalErr must be set")
	}
	if cmd == nil {
		t.Fatal("the TUI must quit on a fatal scan error")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("expected tea.Quit")
	}
}

func TestViewFitsHeightWithBannerAndStatus(t *testing.T) {
	root := &scanner.Node{Name: "/r", IsDir: true, Size: 100}
	for i := 0; i < 50; i++ {
		root.Children = append(root.Children, &scanner.Node{Name: "f", Size: 1, Parent: root})
	}
	m := browsing(root)
	if got := lines(m.View()); got != 20 {
		t.Fatalf("plain view: %d lines, want 20", got)
	}
	m.updateState, m.updateTag = updateReady, "v9"
	m.status = "hello"
	m.toggleMark(root.Children[0])
	if got := lines(m.View()); got != 20 {
		t.Fatalf("view with banner/status/marks: %d lines, want 20", got)
	}
}

func TestViewSanitizesNames(t *testing.T) {
	root := &scanner.Node{Name: "/r", IsDir: true, Size: 10}
	root.Children = []*scanner.Node{{Name: "evil\x1b]0;PWNED\x07\nx", Size: 10, Parent: root}}
	m := browsing(root)
	v := m.View()
	if strings.Contains(v, "\x1b]0;") || strings.Contains(v, "\x07") {
		t.Fatalf("raw OSC sequence in view: %q", v)
	}
}

func TestPlanQuotesPathsAndSanitizesComments(t *testing.T) {
	root := &scanner.Node{Name: "/srv", IsDir: true, Size: 30}
	evil := &scanner.Node{Name: "x\nrm -rf ~", Size: 10, Parent: root}
	quote := &scanner.Node{Name: "it's", Size: 5, Parent: root}
	dir := &scanner.Node{Name: "cache", IsDir: true, Size: 20, Parent: root}
	root.Children = []*scanner.Node{evil, quote, dir}
	m := browsing(root)
	m.toggleMark(evil)
	m.toggleMark(quote)
	m.toggleMark(dir)
	plan := m.planText(time.Unix(0, 0))
	if !strings.Contains(plan, "rm -f -- '/srv/it'\\''s'") {
		t.Errorf("file not single-quoted correctly:\n%s", plan)
	}
	if !strings.Contains(plan, "# IGNORÉ") {
		t.Errorf("a path with a newline must be left out of the script:\n%s", plan)
	}
	if !strings.Contains(plan, "rm -rf -- '/srv/cache'") {
		t.Errorf("dir line missing:\n%s", plan)
	}
	for _, l := range strings.Split(plan, "\n") {
		if strings.HasPrefix(l, "rm -rf ~") {
			t.Fatalf("a file name escaped its comment/quote:\n%s", plan)
		}
	}
	// The script must be valid sh.
	if sh, err := exec.LookPath("sh"); err == nil {
		f := filepath.Join(t.TempDir(), "plan.sh")
		os.WriteFile(f, []byte(plan), 0o600)
		if out, err := exec.Command(sh, "-n", f).CombinedOutput(); err != nil {
			t.Fatalf("sh -n failed: %v %s", err, out)
		}
	}
}

func TestMarkedRootsSkipsCoveredChildren(t *testing.T) {
	root := &scanner.Node{Name: "/r", IsDir: true}
	a := &scanner.Node{Name: "a", IsDir: true, Size: 10, Parent: root}
	b := &scanner.Node{Name: "b", Size: 4, Parent: a}
	a.Children = []*scanner.Node{b}
	root.Children = []*scanner.Node{a}
	m := browsing(root)
	m.toggleMark(b)
	m.toggleMark(a)
	if got := m.markedRoots(); len(got) != 1 || got[0] != a {
		t.Fatalf("markedRoots = %v", got)
	}
	if total, _ := m.markTotals(); total != 10 {
		t.Fatalf("total %d counts the child twice", total)
	}
}

func scanTree(t *testing.T, dir string) scanner.ScanProgress {
	t.Helper()
	var last scanner.ScanProgress
	for p := range scanner.Scan(context.Background(), dir, scanner.ScanOptions{}) {
		last = p
	}
	if last.Root == nil {
		t.Fatalf("scan failed: %v", last.Err)
	}
	return last
}

func modelFromScan(res scanner.ScanProgress) Model {
	m := Model{State: StateScanning, Width: 100, Height: 30}
	model, _ := m.Update(progressMsg{progress: res})
	return model.(Model)
}

func TestDeleteConfirmAndApply(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "junk", "deep"), 0o755)
	os.WriteFile(filepath.Join(tmp, "junk", "deep", "f"), make([]byte, 64*1024), 0o644)
	os.WriteFile(filepath.Join(tmp, "keep"), make([]byte, 8*1024), 0o644)
	m := modelFromScan(scanTree(t, tmp))
	before := m.Root.Size

	// Select junk (largest first) and ask for deletion.
	if m.Entries[0].Name != "junk" {
		t.Fatalf("expected junk first, got %s", m.Entries[0].Name)
	}
	junkSize := m.Entries[0].Size
	model, _ := m.Update(keyMsg("d"))
	m = model.(Model)
	if m.confirm == nil {
		t.Fatal("d must ask for confirmation")
	}
	// Anything but o/y cancels.
	model, _ = m.Update(keyMsg("n"))
	if model.(Model).confirm != nil {
		t.Fatal("n must cancel")
	}
	model, _ = model.(Model).Update(keyMsg("d"))
	model, cmd := model.(Model).Update(keyMsg("o"))
	if cmd == nil {
		t.Fatal("o must start the deletion")
	}
	model, _ = model.(Model).Update(cmd())
	m = model.(Model)
	if _, err := os.Stat(filepath.Join(tmp, "junk")); !os.IsNotExist(err) {
		t.Fatal("junk still on disk")
	}
	if m.Root.Size != before-junkSize || len(m.Root.Children) != 1 {
		t.Fatalf("tree not updated: size %d (want %d), children %d", m.Root.Size, before-junkSize, len(m.Root.Children))
	}
	if !strings.Contains(m.status, "Supprimé") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestReadOnlyRefusesDelete(t *testing.T) {
	root := &scanner.Node{Name: "/r", IsDir: true}
	root.Children = []*scanner.Node{{Name: "a", Size: 1, Parent: root}}
	m := browsing(root)
	m.ReadOnly = true
	model, _ := m.Update(keyMsg("d"))
	if model.(Model).confirm != nil || !model.(Model).statusErr {
		t.Fatal("read-only mode must refuse deletion")
	}
}

func TestRescanGraftsFreshSubtree(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sub")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "a"), make([]byte, 8*1024), 0o644)
	m := modelFromScan(scanTree(t, tmp))
	subNode := m.Entries[0]
	model, _ := m.enter(subNode, 0)
	m = model.(Model)
	rootBefore := m.Root.Size

	os.WriteFile(filepath.Join(sub, "b"), make([]byte, 128*1024), 0o644)
	model, cmd := m.Update(keyMsg("r"))
	m = model.(Model)
	if m.rescanning != subNode || cmd == nil {
		t.Fatal("r must start a rescan of the current directory")
	}
	var res rescanMsg
	for p := range scanner.Scan(context.Background(), sub, scanner.ScanOptions{}) {
		if p.Done {
			res = rescanMsg{target: subNode, progress: p}
		}
	}
	model, _ = m.Update(res)
	m = model.(Model)
	if m.rescanning != nil || len(subNode.Children) != 2 || m.Root.Size <= rootBefore {
		t.Fatalf("graft failed: children=%d root %d -> %d", len(subNode.Children), rootBefore, m.Root.Size)
	}
	for _, c := range subNode.Children {
		if c.Parent != subNode {
			t.Fatal("grafted children must point to the kept node")
		}
	}
	if m.Root.Size != m.Root.OwnSize+subNode.Size {
		t.Fatalf("ancestor size not adjusted: root=%d own=%d sub=%d", m.Root.Size, m.Root.OwnSize, subNode.Size)
	}
}

func TestTopFilesOverlayRevealsFile(t *testing.T) {
	root := &scanner.Node{Name: "/r", IsDir: true}
	d := &scanner.Node{Name: "d", IsDir: true, Parent: root, Size: 100}
	big := &scanner.Node{Name: "big", Size: 100, Parent: d}
	d.Children = []*scanner.Node{big}
	root.Children = []*scanner.Node{d, {Name: "small", Size: 1, Parent: root}}
	root.Size = 101
	m := browsing(root)
	model, _ := m.Update(keyMsg("f"))
	m = model.(Model)
	if m.mode != viewTopFiles || !strings.Contains(m.View(), "d/big") {
		t.Fatalf("top files view wrong:\n%s", m.View())
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.mode != viewList || m.Current != d || m.Entries[m.Selected] != big {
		t.Fatal("enter must reveal the file in its directory")
	}
}

func TestGrowthColumnAndSort(t *testing.T) {
	root := &scanner.Node{Name: "/r", IsDir: true}
	grew := &scanner.Node{Name: "grew", IsDir: true, Size: 10 << 20, Parent: root}
	big := &scanner.Node{Name: "big", IsDir: true, Size: 50 << 20, Parent: root}
	root.Children = []*scanner.Node{big, grew}
	root.Size = 60 << 20
	prev := &snapshot.Snapshot{Time: time.Now().Add(-time.Hour), Sizes: map[string]int64{"": 52 << 20, "grew": 2 << 20, "big": 50 << 20}}
	m := browsing(root)
	m.Prev = prev
	if !strings.Contains(m.View(), "+8.0 MiB") {
		t.Fatalf("growth column missing:\n%s", m.View())
	}
	model, _ := m.Update(keyMsg("c"))
	m = model.(Model)
	if m.Entries[0] != grew {
		t.Fatal("c must sort by growth")
	}
}

func TestProgressShowsPercentWhenExpectedKnown(t *testing.T) {
	m := Model{State: StateScanning, Width: 100, Height: 25, Path: "/var",
		Progress: scanner.ScanProgress{Files: 400, Dirs: 100, Expected: 1000, Elapsed: 2 * time.Second}}
	v := m.View()
	if !strings.Contains(v, " 50 %") || !strings.Contains(v, "restantes") {
		t.Fatalf("progress percent/ETA missing:\n%s", v)
	}
}

func TestEnterRefreshesDiskInfoAndIgnoresStale(t *testing.T) {
	root := &scanner.Node{Name: "/", IsDir: true}
	child := &scanner.Node{Name: "mnt", IsDir: true, Parent: root}
	root.Children = []*scanner.Node{child}
	m := browsing(root)
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	// A late answer for the previous directory must be ignored.
	model, _ = m.Update(diskInfoMsg{path: "/", Total: 1, Used: 1})
	if model.(Model).diskTotal == 1 {
		t.Fatal("stale disk info applied")
	}
	model, _ = m.Update(diskInfoMsg{path: "/mnt", Total: 42, Used: 1})
	if model.(Model).diskTotal != 42 {
		t.Fatal("disk info for the current directory not applied")
	}
}
