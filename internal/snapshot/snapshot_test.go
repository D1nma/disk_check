package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/D1nma/disk_check/internal/scanner"
)

const mib = 1 << 20

// tree: /srv (root) → logs/ (app.log), data/, tiny
func tree(appLog, data int64) *scanner.Node {
	root := &scanner.Node{Name: "/srv", IsDir: true}
	logs := &scanner.Node{Name: "logs", IsDir: true, Parent: root}
	app := &scanner.Node{Name: "app\tlog", Size: appLog, Parent: logs}
	logs.Children = []*scanner.Node{app}
	logs.Size = appLog
	dataN := &scanner.Node{Name: "data", IsDir: true, Size: data, Parent: root}
	tiny := &scanner.Node{Name: "tiny", Size: 4096, Parent: root}
	root.Children = []*scanner.Node{logs, dataN, tiny}
	root.Size = logs.Size + dataN.Size + tiny.Size
	return root
}

func TestFromTreeThresholdAndRelPath(t *testing.T) {
	root := tree(5*mib, 2*mib)
	s := FromTree(root, "/srv", "global", 42, time.Unix(1000, 0))
	if _, ok := s.Sizes["tiny"]; ok {
		t.Error("entries below MinBytes must not be recorded")
	}
	if s.Sizes["logs/app\tlog"] != 5*mib || s.Sizes[""] != root.Size {
		t.Errorf("unexpected sizes: %v", s.Sizes)
	}
	if got := RelPath(root.Children[0].Children[0]); got != "logs/app\tlog" {
		t.Errorf("RelPath = %q", got)
	}
}

func TestSaveLatestRoundTripAndPrune(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	for i := 0; i < Keep+3; i++ {
		s := FromTree(tree(int64(i+1)*mib, 2*mib), "/srv", "global", int64(i), time.Unix(int64(1000+i), int64(i)))
		if err := st.Save(s); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Latest("/srv", "global")
	if err != nil || got == nil {
		t.Fatalf("Latest: %v %v", got, err)
	}
	if got.Entries != int64(Keep+2) || got.Sizes["logs/app\tlog"] != int64(Keep+3)*mib || got.ID != "/srv" {
		t.Errorf("wrong latest snapshot: %+v", got)
	}
	files, _ := filepath.Glob(filepath.Join(st.keyDir("/srv", "global"), "*.snap.gz"))
	if len(files) != Keep {
		t.Errorf("kept %d snapshots, want %d", len(files), Keep)
	}
	if other, err := st.Latest("/srv", "partition"); other != nil || err != nil {
		t.Errorf("another mode must not share history: %v %v", other, err)
	}
}

func TestLatestSkipsCorruptFile(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	if err := st.Save(FromTree(tree(mib, mib), "/srv", "global", 1, time.Unix(1, 0))); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(st.keyDir("/srv", "global"), "99999999999999999.snap.gz")
	if err := os.WriteFile(bad, []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := st.Latest("/srv", "global")
	if err != nil || got == nil || got.Entries != 1 {
		t.Fatalf("corrupt newest file must be skipped: %+v %v", got, err)
	}
}

func TestDeltaAndTopGrowth(t *testing.T) {
	prev := FromTree(tree(2*mib, 3*mib), "/srv", "global", 0, time.Unix(0, 0))
	now := tree(10*mib, 3*mib)
	logs := now.Children[0]
	if d, exact := prev.Delta(logs); d != 8*mib || !exact {
		t.Errorf("Delta(logs) = %d %v", d, exact)
	}
	if d, exact := prev.Delta(now.Children[1]); d != 0 || !exact {
		t.Errorf("Delta(data) = %d %v", d, exact)
	}
	// New big directory: upper bound, not exact.
	big := &scanner.Node{Name: "new", IsDir: true, Size: 4 * mib, Parent: now}
	now.Children = append(now.Children, big)
	if d, exact := prev.Delta(big); d != 4*mib || exact {
		t.Errorf("Delta(new) = %d %v", d, exact)
	}
	top := prev.TopGrowth(now, 5)
	if len(top) == 0 || top[0].Path != "logs/app\tlog" {
		t.Fatalf("the file explaining the growth must lead, got %+v", top)
	}
	for _, g := range top {
		if g.Path == "logs" {
			t.Errorf("logs is redundant with logs/app\\tlog: %+v", top)
		}
	}
}
