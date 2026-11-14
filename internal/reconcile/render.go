package reconcile

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/D1nma/disk_check/internal/display"
	"github.com/D1nma/disk_check/internal/textsafe"
)

// defaultTop is how many deleted files are listed when top <= 0.
const defaultTop = 10

// Render writes a human report in French. top limits how many deleted
// files are listed (<=0: 10).
func Render(w io.Writer, r Report, top int) {
	var b strings.Builder
	for _, l := range Lines(r, top) {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	_, _ = io.WriteString(w, b.String())
}

// Lines returns the same content as Render as plain lines (no trailing
// newline), for embedding in a TUI panel. Every path and process name is
// passed through textsafe.Clean, so no line contains control characters.
func Lines(r Report, top int) []string {
	if top <= 0 {
		top = defaultTop
	}
	fs := r.FS
	mp := safe(fs.MountPoint)
	var out []string

	out = append(out, header(fs))
	if fs.FSType == "overlay" || fs.FSType == "fuse.fuse-overlayfs" {
		out = append(out, "  overlay : les chiffres de df sont ceux du système de fichiers de la couche supérieure (upperdir)")
	}

	labels := []string{"Utilisé (df)", "Trouvé par le scan"}
	values := []int64{fs.Used, r.Scanned}
	if r.Covers && r.Gap >= 0 {
		labels = append(labels, "Écart")
		values = append(values, r.Gap)
	}
	out = append(out, table("  ", labels, values)...)

	switch {
	case !r.Covers:
		out = append(out, fmt.Sprintf(
			"  Écart non calculable : le scan n'a pas démarré au point de montage (%s) → relancer depuis %s pour comparer avec df",
			mp, mp))
		out = append(out, deletedSection(r.Deleted, r.DeletedTotal, top)...)
	case r.Gap < 0:
		out = append(out, fmt.Sprintf(
			"  Le scan trouve %s de plus que df : compression, déduplication, ou fichiers créés pendant le scan",
			size(-r.Gap)))
		out = append(out, deletedSection(r.Deleted, r.DeletedTotal, top)...)
	default:
		out = append(out, breakdown(r, top)...)
	}

	if r.Unreadable == 1 {
		out = append(out, "  1 dossier illisible : sa taille manque au scan → relancer avec sudo")
	} else if r.Unreadable > 1 {
		out = append(out, fmt.Sprintf("  %d dossiers illisibles : leur taille manque au scan → relancer avec sudo", r.Unreadable))
	}
	if r.Hidden == 1 {
		out = append(out, "  1 processus non inspecté ("+hiddenHint()+")")
	} else if r.Hidden > 1 {
		out = append(out, fmt.Sprintf("  %d processus non inspectés (%s)", r.Hidden, hiddenHint()))
	}
	if l, ok := inodeLine(fs); ok {
		out = append(out, l)
	}
	if l, ok := reservedLine(fs); ok {
		out = append(out, l)
	}
	return out
}

// header is "Système de fichiers : /mnt (ext4, /dev/sda1)".
func header(fs FSInfo) string {
	h := "Système de fichiers : " + safe(fs.MountPoint)
	var details []string
	if fs.FSType != "" {
		details = append(details, textsafe.Clean(fs.FSType))
	}
	if fs.Source != "" && fs.Source != fs.FSType {
		details = append(details, textsafe.Clean(fs.Source))
	}
	if len(details) > 0 {
		h += " (" + strings.Join(details, ", ") + ")"
	}
	return h
}

// breakdown splits a non-negative gap into deleted-but-open files and the
// unexplained remainder, sizes right-aligned in one column.
func breakdown(r Report, top int) []string {
	const indent = "    "
	delSize := size(r.DeletedTotal)
	w := len(delSize)
	var unexSize string
	if r.Unexplained > 0 {
		unexSize = size(r.Unexplained)
		w = max(w, len(unexSize))
	}
	sub := strings.Repeat(" ", len(indent)+w+2) // text column of the breakdown

	var out []string
	if len(r.Deleted) == 0 {
		out = append(out, indent+padLeft(delSize, w)+"  fichiers supprimés mais encore ouverts (aucun)")
	} else {
		out = append(out, fmt.Sprintf("%s%s  fichiers supprimés mais encore ouverts (%d)",
			indent, padLeft(delSize, w), len(r.Deleted)))
		out = append(out, deletedList(sub, r.Deleted, top)...)
		if r.Unexplained <= 0 {
			out = append(out, sub+"(ces fichiers expliquent tout l'écart)")
		}
	}

	if r.Unexplained > 0 {
		// Under 1 % of the used space, the FS's own bookkeeping is the
		// obvious culprit: don't send the admin hunting.
		if r.Unexplained*100 < r.FS.Used {
			out = append(out, indent+padLeft(unexSize, w)+"  inexpliqué : négligeable (métadonnées du FS)")
			return out
		}
		causes := "métadonnées/journal du FS, fichiers masqués sous un point de montage"
		snap := snapshotHint(r.FS)
		if snap != "" {
			causes = "snapshots, " + causes
		}
		out = append(out, indent+padLeft(unexSize, w)+"  inexpliqué : "+causes)
		if snap != "" {
			out = append(out, sub+"→ snapshots : "+snap)
		}
		mp := safe(r.FS.MountPoint)
		out = append(out, sub+"→ fichiers masqués : mount --bind "+mp+" /mnt puis scanner /mnt")
	}
	return out
}

// deletedSection is the stand-alone form of the deleted-files list, used
// when there is no gap to break down.
func deletedSection(files []DeletedFile, total int64, top int) []string {
	if len(files) == 0 {
		return []string{"  Fichiers supprimés mais encore ouverts : aucun"}
	}
	out := []string{fmt.Sprintf("  Fichiers supprimés mais encore ouverts : %s (%d)", size(total), len(files))}
	return append(out, deletedList("      ", files, top)...)
}

// deletedList lists up to top files under indent, then a summary of the
// rest and how to free the biggest one.
func deletedList(indent string, files []DeletedFile, top int) []string {
	n := min(top, len(files))
	sizes := make([]string, n)
	w := 0
	for i := range n {
		sizes[i] = size(files[i].Size)
		w = max(w, len(sizes[i]))
	}
	out := make([]string, 0, n+2)
	for i := range n {
		f := &files[i]
		out = append(out, fmt.Sprintf("%s%s  %s  %s", indent, padLeft(sizes[i], w), holder(f), safe(f.Path)))
	}
	if rest := files[n:]; len(rest) > 0 {
		var restSize int64
		for _, f := range rest {
			restSize += f.Size
		}
		noun := "autres"
		if len(rest) == 1 {
			noun = "autre"
		}
		out = append(out, fmt.Sprintf("%s… et %d %s (%s)", indent, len(rest), noun, size(restSize)))
	}
	// truncate(1) rather than ": >": it works under sudo, where a shell
	// redirection would run with the caller's privileges.
	out = append(out, fmt.Sprintf(
		"%s→ redémarrer le processus, ou vider sans redémarrer (contenu perdu) : truncate -s 0 /proc/%d/fd/%d",
		indent, files[0].PID, files[0].FD))
	return out
}

// holder is "java (pid 1234, fd 7)" or "java (pid 1234, fd 7, +2 autres)".
func holder(f *DeletedFile) string {
	comm := safe(f.Comm)
	extra := ""
	switch others := f.Holders - 1; {
	case others == 1:
		extra = ", +1 autre"
	case others > 1:
		extra = fmt.Sprintf(", +%d autres", others)
	}
	return fmt.Sprintf("%s (pid %d, fd %d%s)", comm, f.PID, f.FD, extra)
}

// snapshotHint returns the command that lists snapshots on filesystems
// that have them, or "".
func snapshotHint(fs FSInfo) string {
	switch fs.FSType {
	case "btrfs":
		return "btrfs subvolume list -s " + safe(fs.MountPoint)
	case "zfs":
		if fs.Source != "" {
			return "zfs list -t snapshot -o name,used -r " + textsafe.Clean(fs.Source)
		}
		return "zfs list -t snapshot -o name,used"
	}
	return ""
}

// inodeLine warns when inodes, not bytes, are about to run out: writes then
// fail with "No space left on device" although df shows free space.
func inodeLine(fs FSInfo) (string, bool) {
	if fs.Inodes == 0 || fs.InodesFree > fs.Inodes {
		return "", false
	}
	used := fs.Inodes - fs.InodesFree
	if used*10 < fs.Inodes*9 { // below 90 %
		return "", false
	}
	pct := float64(used) * 100 / float64(fs.Inodes)
	return fmt.Sprintf("  Inodes : %s utilisés (%d libres) — « No space left on device » possible malgré l'espace libre",
		percent(pct), fs.InodesFree), true
}

// reservedLine explains free blocks that non-root users cannot use.
func reservedLine(fs FSInfo) (string, bool) {
	if fs.Reserved <= 0 {
		return "", false
	}
	pct := ""
	if fs.Total > 0 {
		pct = percent(float64(fs.Reserved)*100/float64(fs.Total)) + " — "
	}
	switch fs.FSType {
	case "ext2", "ext3", "ext4":
		return fmt.Sprintf("  Réservé root : %s (%sinvisible pour les utilisateurs non root ; ajustable : tune2fs -m)",
			size(fs.Reserved), pct), true
	case "btrfs":
		// btrfs has no root reserve: f_bfree - f_bavail comes from the
		// RAID profile and the metadata reserve.
		return fmt.Sprintf("  Libre mais non disponible : %s (estimation btrfs : profil RAID, réserve de métadonnées)",
			size(fs.Reserved)), true
	default:
		return fmt.Sprintf("  Réservé : %s (%sblocs libres inutilisables par les utilisateurs non root)",
			size(fs.Reserved), pct), true
	}
}

// table renders label/size rows with labels left-aligned and sizes
// right-aligned.
func table(indent string, labels []string, values []int64) []string {
	lw, vw := 0, 0
	sizes := make([]string, len(values))
	for i := range labels {
		lw = max(lw, utf8.RuneCountInString(labels[i]))
		sizes[i] = size(values[i])
		vw = max(vw, len(sizes[i]))
	}
	out := make([]string, len(labels))
	for i := range labels {
		out[i] = indent + padRight(labels[i], lw) + "  " + padLeft(sizes[i], vw)
	}
	return out
}

// size formats a byte count; display.FormatSize does not handle negatives.
func size(n int64) string {
	if n < 0 {
		return "-" + display.FormatSize(-n)
	}
	return display.FormatSize(n)
}

// percent is "5 %" (French spacing), with one decimal below 1 %.
func percent(p float64) string {
	if p < 1 {
		return fmt.Sprintf("%.1f %%", p)
	}
	return fmt.Sprintf("%.0f %%", p)
}

// safe sanitises an untrusted name for display; empty becomes "?".
func safe(s string) string {
	if s == "" {
		return "?"
	}
	return textsafe.Clean(s)
}

func padRight(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func padLeft(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return strings.Repeat(" ", width-n) + s
	}
	return s
}

// hiddenHint explains why processes could not be inspected: missing
// privileges, or (already root) another namespace / kernel restrictions.
func hiddenHint() string {
	if os.Geteuid() == 0 {
		return "autre espace de noms ou restrictions du noyau"
	}
	return "relancez en root pour une liste complète"
}
