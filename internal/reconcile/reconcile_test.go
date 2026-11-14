package reconcile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const gib = int64(1) << 30

func ext4FS() FSInfo {
	return FSInfo{
		MountPoint: "/",
		Source:     "/dev/sda1",
		FSType:     "ext4",
		Dev:        2049,
		Total:      100 * gib,
		Used:       92 * gib,
		Avail:      3 * gib,
		Reserved:   5 * gib,
		Inodes:     6_000_000,
		InodesFree: 5_000_000,
	}
}

func sampleDeleted() []DeletedFile {
	return []DeletedFile{
		{PID: 800, Comm: "rsyslogd", FD: 9, Holders: 1, Path: "/var/log/syslog", Size: 4 * gib, Dev: 2049, Ino: 3},
		{PID: 1234, Comm: "java", FD: 7, Holders: 1, Path: "/var/log/app.log", Size: 12 * gib, Dev: 2049, Ino: 1},
		{PID: 99, Comm: "nginx", FD: 3, Holders: 3, Path: "/var/log/nginx/access.log", Size: 8 * gib, Dev: 2049, Ino: 2},
	}
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

func mustContain(t *testing.T, out string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
}

func mustNotContain(t *testing.T, out string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(out, s) {
			t.Errorf("output unexpectedly contains %q:\n%s", s, out)
		}
	}
}

func TestBuildArithmeticAndOrder(t *testing.T) {
	in := sampleDeleted()
	r := Build(ext4FS(), 60*gib, true, 3, in, 2)

	if r.DeletedTotal != 24*gib {
		t.Errorf("DeletedTotal = %d, want %d", r.DeletedTotal, 24*gib)
	}
	if r.Gap != 32*gib {
		t.Errorf("Gap = %d, want %d", r.Gap, 32*gib)
	}
	if r.Unexplained != 8*gib {
		t.Errorf("Unexplained = %d, want %d", r.Unexplained, 8*gib)
	}
	if r.Hidden != 2 || r.Unreadable != 3 || !r.Covers || r.Scanned != 60*gib {
		t.Errorf("fields not carried over: %+v", r)
	}
	if got := [3]int{r.Deleted[0].PID, r.Deleted[1].PID, r.Deleted[2].PID}; got != [3]int{1234, 99, 800} {
		t.Errorf("Deleted not sorted by size desc: %v", got)
	}
	if in[0].PID != 800 {
		t.Error("Build reordered the caller's slice")
	}
}

func TestBuildNegativeGap(t *testing.T) {
	fs := ext4FS()
	fs.Used = 50 * gib
	r := Build(fs, 53*gib, true, 0, nil, 0)
	if r.Gap != -3*gib || r.Unexplained != -3*gib || r.DeletedTotal != 0 || r.Deleted != nil {
		t.Errorf("unexpected report: %+v", r)
	}
}

func TestLinesCoversExt4(t *testing.T) {
	r := Build(ext4FS(), 60*gib, true, 3, sampleDeleted(), 2)
	lines := Lines(r, 0)
	out := joined(lines)
	t.Logf("\n%s", out)

	mustContain(t, out,
		"Système de fichiers : / (ext4, /dev/sda1)",
		"Utilisé (df)", "Trouvé par le scan", "Écart",
		"24.0 GiB  fichiers supprimés mais encore ouverts (3)",
		"12.0 GiB  java (pid 1234, fd 7)  /var/log/app.log",
		"nginx (pid 99, fd 3, +2 autres)  /var/log/nginx/access.log",
		"truncate -s 0 /proc/1234/fd/7", // biggest file first
		"8.0 GiB  inexpliqué : métadonnées/journal du FS, fichiers masqués sous un point de montage",
		"mount --bind / /mnt",
		"3 dossiers illisibles",
		"2 processus non inspectés (" + hiddenHint() + ")",
		"Réservé root : 5.0 GiB (5 % — invisible pour les utilisateurs non root ; ajustable : tune2fs -m)",
	)
	mustNotContain(t, out, "snapshots", "Inodes", "non calculable")

	// The three df/scan/gap rows have their sizes right-aligned.
	var ends []int
	for _, l := range lines[1:4] {
		ends = append(ends, utf8.RuneCountInString(l))
	}
	if ends[0] != ends[1] || ends[1] != ends[2] {
		t.Errorf("table rows not aligned (rune widths %v):\n%s", ends, out)
	}
	// The breakdown sizes end in the same column too.
	var del, unex string
	for _, l := range lines {
		if strings.Contains(l, "fichiers supprimés mais encore ouverts") {
			del = l
		}
		if strings.Contains(l, "inexpliqué") {
			unex = l
		}
	}
	if strings.Index(del, "B  ") != strings.Index(unex, "B  ") {
		t.Errorf("breakdown sizes not aligned:\n%s\n%s", del, unex)
	}
}

func TestRenderMatchesLines(t *testing.T) {
	r := Build(ext4FS(), 60*gib, true, 1, sampleDeleted(), 1)
	var buf bytes.Buffer
	Render(&buf, r, 2)
	want := joined(Lines(r, 2)) + "\n"
	if buf.String() != want {
		t.Errorf("Render != Lines:\n%q\n%q", buf.String(), want)
	}
	for _, l := range Lines(r, 2) {
		if strings.ContainsRune(l, '\n') {
			t.Errorf("line contains a newline: %q", l)
		}
	}
	mustContain(t, buf.String(), "1 dossier illisible : sa taille", "1 processus non inspecté (")
}

func TestLinesNotCovering(t *testing.T) {
	fs := ext4FS()
	fs.MountPoint = "/srv"
	r := Build(fs, 10*gib, false, 0, sampleDeleted(), 0)
	out := joined(Lines(r, 0))
	t.Logf("\n%s", out)

	mustContain(t, out,
		"Écart non calculable : le scan n'a pas démarré au point de montage (/srv)",
		"Fichiers supprimés mais encore ouverts : 24.0 GiB (3)",
		"12.0 GiB  java (pid 1234, fd 7)  /var/log/app.log",
		"truncate -s 0 /proc/1234/fd/7",
		"Réservé root : 5.0 GiB",
	)
	mustNotContain(t, out, "  Écart  ", "inexpliqué", "de plus que df")
	for _, l := range Lines(r, 0) {
		if strings.HasPrefix(strings.TrimSpace(l), "Écart ") && !strings.Contains(l, "non calculable") {
			t.Errorf("gap row printed although the scan does not cover the FS: %q", l)
		}
	}
}

func TestLinesNegativeGap(t *testing.T) {
	fs := ext4FS()
	fs.Used = 50 * gib
	r := Build(fs, 53*gib, true, 0, sampleDeleted()[:1], 0)
	out := joined(Lines(r, 0))
	t.Logf("\n%s", out)

	mustContain(t, out,
		"Le scan trouve 3.0 GiB de plus que df : compression, déduplication, ou fichiers créés pendant le scan",
		"Fichiers supprimés mais encore ouverts : 4.0 GiB (1)",
	)
	mustNotContain(t, out, "inexpliqué", "-3", "Écart  ")
}

func TestLinesNoDeleted(t *testing.T) {
	r := Build(ext4FS(), 60*gib, true, 0, nil, 0)
	out := joined(Lines(r, 0))
	t.Logf("\n%s", out)

	mustContain(t, out,
		"0 B  fichiers supprimés mais encore ouverts (aucun)",
		"32.0 GiB  inexpliqué :",
	)
	mustNotContain(t, out, "truncate", "processus non inspecté", "illisible")
}

func TestLinesDeletedExplainEverything(t *testing.T) {
	r := Build(ext4FS(), 80*gib, true, 0, sampleDeleted(), 0) // gap 12, deleted 24
	out := joined(Lines(r, 0))
	mustContain(t, out, "(ces fichiers expliquent tout l'écart)")
	mustNotContain(t, out, "inexpliqué")
}

func TestLinesNegligibleUnexplained(t *testing.T) {
	fs := ext4FS()
	r := Build(fs, fs.Used-gib/2, true, 0, nil, 0) // 0.5 GiB of 92 GiB
	out := joined(Lines(r, 0))
	mustContain(t, out, "inexpliqué : négligeable (métadonnées du FS)")
	mustNotContain(t, out, "mount --bind")
}

func TestLinesReserved(t *testing.T) {
	xfs := ext4FS()
	xfs.FSType, xfs.Source = "xfs", "/dev/nvme0n1p2"
	out := joined(Lines(Build(xfs, 60*gib, true, 0, nil, 0), 0))
	mustContain(t, out, "Réservé : 5.0 GiB (5 % — blocs libres inutilisables par les utilisateurs non root)")
	mustNotContain(t, out, "tune2fs", "Réservé root")

	for _, typ := range []string{"ext2", "ext3"} {
		fs := ext4FS()
		fs.FSType = typ
		mustContain(t, joined(Lines(Build(fs, 60*gib, true, 0, nil, 0), 0)), "tune2fs -m")
	}

	none := ext4FS()
	none.Reserved = 0
	mustNotContain(t, joined(Lines(Build(none, 60*gib, true, 0, nil, 0), 0)), "Réservé")

	small := ext4FS()
	small.Reserved = gib / 2 // 0.5 % of 100 GiB
	mustContain(t, joined(Lines(Build(small, 60*gib, true, 0, nil, 0), 0)), "(0.5 % —")
}

func TestLinesSnapshotsAndOverlay(t *testing.T) {
	btrfs := ext4FS()
	btrfs.FSType, btrfs.MountPoint = "btrfs", "/data"
	out := joined(Lines(Build(btrfs, 60*gib, true, 0, nil, 0), 0))
	mustContain(t, out, "inexpliqué : snapshots, ", "→ snapshots : btrfs subvolume list -s /data",
		"Libre mais non disponible : 5.0 GiB")
	mustNotContain(t, out, "tune2fs", "Réservé")

	zfs := ext4FS()
	zfs.FSType, zfs.Source, zfs.Reserved = "zfs", "rpool/ROOT/ubuntu", 0
	mustContain(t, joined(Lines(Build(zfs, 60*gib, true, 0, nil, 0), 0)),
		"inexpliqué : snapshots, ", "zfs list -t snapshot -o name,used -r rpool/ROOT/ubuntu")

	ov := ext4FS()
	ov.FSType, ov.Source = "overlay", "overlay"
	out = joined(Lines(Build(ov, 60*gib, true, 0, nil, 0), 0))
	mustContain(t, out, "Système de fichiers : / (overlay)\n", "couche supérieure")
	mustNotContain(t, out, "snapshots")
}

func TestLinesTopLimit(t *testing.T) {
	var files []DeletedFile
	for i := range 12 {
		files = append(files, DeletedFile{PID: 100 + i, Comm: "w", FD: 3, Holders: 1,
			Path: "/tmp/f", Size: int64(12-i) * gib, Ino: uint64(i)})
	}
	r := Build(ext4FS(), 10*gib, true, 0, files, 0)

	count := func(lines []string) int {
		n := 0
		for _, l := range lines {
			if strings.Contains(l, "(pid ") {
				n++
			}
		}
		return n
	}
	lines := Lines(r, 0)
	if n := count(lines); n != defaultTop {
		t.Errorf("top<=0 listed %d files, want %d", n, defaultTop)
	}
	mustContain(t, joined(lines), "… et 2 autres (3.0 GiB)") // 2 GiB + 1 GiB
	lines = Lines(r, 11)
	if n := count(lines); n != 11 {
		t.Errorf("top=11 listed %d files", n)
	}
	mustContain(t, joined(lines), "… et 1 autre (1.0 GiB)")
}

func TestLinesInodes(t *testing.T) {
	fs := ext4FS()
	fs.Inodes, fs.InodesFree = 1000, 20
	mustContain(t, joined(Lines(Build(fs, 60*gib, true, 0, nil, 0), 0)),
		"Inodes : 98 % utilisés (20 libres)")
}

func TestLinesSanitized(t *testing.T) {
	evil := "\x1b]0;x\x07"
	fs := ext4FS()
	fs.MountPoint = "/mnt/" + evil
	fs.Source = "/dev/" + evil
	fs.FSType = "fuse." + evil
	deleted := []DeletedFile{{
		PID: 4242, Comm: "evil" + evil, FD: 5, Holders: 1,
		Path: "/tmp/" + evil + "\nfake line", Size: gib,
	}}
	for _, covers := range []bool{true, false} {
		r := Build(fs, 10*gib, covers, 0, deleted, 0)
		var buf bytes.Buffer
		Render(&buf, r, 0)
		out := buf.Bytes()
		for _, c := range []byte{0x1b, 0x07} {
			if bytes.IndexByte(out, c) >= 0 {
				t.Errorf("covers=%v: output contains byte %#x:\n%q", covers, c, out)
			}
		}
		for _, l := range Lines(r, 0) {
			if strings.ContainsAny(l, "\x1b\x07\n") {
				t.Errorf("covers=%v: unsafe line %q", covers, l)
			}
		}
		mustContain(t, buf.String(), `/tmp/?]0;x?\nfake line`, "evil?]0;x? (pid 4242, fd 5)")
	}
}

func TestFromCounts(t *testing.T) {
	got := fromCounts(1000, 300, 250, 4096)
	want := FSInfo{Total: 1000 * 4096, Used: 700 * 4096, Avail: 250 * 4096, Reserved: 50 * 4096}
	if got != want {
		t.Errorf("fromCounts = %+v, want %+v", got, want)
	}
	if got := fromCounts(10, 20, 30, 512); got.Used != 0 || got.Reserved != 0 {
		t.Errorf("inconsistent counts not clamped: %+v", got)
	}
	if got := fromCounts(10, 5, 5, 0); got != (FSInfo{}) {
		t.Errorf("zero block size: %+v", got)
	}
}

func TestStartsAtMount(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	fs := FSInfo{MountPoint: dir}
	for _, root := range []string{dir, dir + "/", link, dir + "/sub/.."} {
		if !StartsAtMount(root, fs) {
			t.Errorf("StartsAtMount(%q, %q) = false", root, dir)
		}
	}
	if StartsAtMount(filepath.Join(dir, "sub"), fs) {
		t.Error("a subdirectory must not cover the filesystem")
	}
	if StartsAtMount(dir, FSInfo{}) {
		t.Error("unknown mount point must not be covered")
	}
}
