package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/D1nma/disk_check/internal/display"
	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/textsafe"
	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// ── Marks and cleanup plan ───────────────────────────────────────────────────

func (m *Model) toggleMark(n *scanner.Node) {
	if n == nil || n == m.Root {
		return
	}
	if m.marks == nil {
		m.marks = make(map[*scanner.Node]bool)
	}
	if m.marks[n] {
		delete(m.marks, n)
		for i, x := range m.markOrder {
			if x == n {
				m.markOrder = append(m.markOrder[:i], m.markOrder[i+1:]...)
				break
			}
		}
		return
	}
	m.marks[n] = true
	m.markOrder = append(m.markOrder, n)
}

// markedRoots returns the marked nodes that have no marked ancestor (a
// marked directory already covers its marked descendants), in mark order.
func (m Model) markedRoots() []*scanner.Node {
	var out []*scanner.Node
	for _, n := range m.markOrder {
		covered := false
		for a := n.Parent; a != nil; a = a.Parent {
			if m.marks[a] {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, n)
		}
	}
	return out
}

func (m Model) markTotals() (total, free int64) {
	for _, n := range m.markedRoots() {
		total += n.Size
		free += scanner.Freeable(n, m.pinned)
	}
	return total, free
}

// shellQuote quotes s for POSIX sh. Single quotes keep every byte literal,
// including newlines; an embedded quote becomes '\”.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// planText renders the cleanup plan as a reviewable shell script. Comment
// lines only contain sanitised text so a crafted file name cannot break
// out of a comment.
func (m Model) planText(now time.Time) string {
	marked := m.markedRoots()
	total, free := m.markTotals()
	host, _ := os.Hostname()
	if m.Imported {
		host = strings.TrimSuffix(m.hostPrefix, ":")
		if host == "" {
			host = "(import)"
		}
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "# Plan de nettoyage généré par disk-explorer %s le %s\n", textsafe.Clean(m.Version), now.Format("2006-01-02 15:04:05"))
	if m.Root != nil {
		fmt.Fprintf(&b, "# Hôte : %s — racine analysée : %s\n", textsafe.Clean(host), textsafe.Clean(m.Root.Path()))
	}
	fmt.Fprintf(&b, "# %d élément(s), %s au total, environ %s réellement libérés\n", len(marked), display.FormatSize(total), display.FormatSize(free))
	b.WriteString("# RELISEZ AVANT D'EXÉCUTER. Les suggestions spécifiques (commandes dédiées)\n")
	b.WriteString("# sont préférables à rm quand elles existent.\n")
	b.WriteString("set -eu\n")
	for _, n := range marked {
		path := n.Path()
		b.WriteString("\n")
		fmt.Fprintf(&b, "# %s — %s\n", display.FormatSize(n.Size), textsafe.Clean(path))
		if f := scanner.Freeable(n, m.pinned); f < n.Size {
			fmt.Fprintf(&b, "# libère seulement %s (liens durs ailleurs)\n", display.FormatSize(f))
		}
		if m.Hooks.Annotate != nil {
			if label, cleanup, ok := m.Hooks.Annotate(path, n.IsDir); ok {
				if label != "" {
					fmt.Fprintf(&b, "# %s\n", textsafe.Clean(label))
				}
				if cleanup != "" {
					fmt.Fprintf(&b, "# suggestion : %s\n", textsafe.Clean(cleanup))
				}
			}
		}
		if hasControl(path) {
			// Valid inside single quotes, but a reviewer would see a fake
			// command on the next line: leave it to a human.
			b.WriteString("# IGNORÉ : le chemin contient des caractères de contrôle (saut de ligne…),\n")
			b.WriteString("# à supprimer manuellement après vérification.\n")
			continue
		}
		if n.IsDir {
			fmt.Fprintf(&b, "rm -rf -- %s\n", shellQuote(path))
		} else {
			fmt.Fprintf(&b, "rm -f -- %s\n", shellQuote(path))
		}
	}
	return b.String()
}

// writePlan writes the plan to $DISK_EXPLORER_PLAN_DIR (default: the
// current directory) and returns its path.
func (m Model) writePlan() (string, error) {
	dir := os.Getenv("DISK_EXPLORER_PLAN_DIR")
	if dir == "" {
		dir = "."
	}
	now := time.Now()
	name := filepath.Join(dir, "disk-explorer-plan-"+now.Format("2006-01-02_15-04-05")+".sh")
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(m.planText(now)); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(name); err == nil {
		name = abs
	}
	return name, nil
}

// ── Deletion ─────────────────────────────────────────────────────────────────

func (m *Model) askDelete(nodes []*scanner.Node) {
	if m.Imported {
		m.status, m.statusErr = "Scan distant ou importé : suppression impossible d'ici — marquez puis [m] [x] pour un plan à exécuter sur l'hôte", true
		return
	}
	if m.ReadOnly {
		m.status, m.statusErr = "Mode lecture seule (--read-only) : suppression désactivée", true
		return
	}
	var keep []*scanner.Node
	for _, n := range nodes {
		if n != nil && n != m.Root && n.Parent != nil {
			keep = append(keep, n)
		}
	}
	if len(keep) == 0 {
		m.status, m.statusErr = "Impossible de supprimer la racine analysée", true
		return
	}
	c := &confirmState{nodes: keep}
	for _, n := range keep {
		c.total += n.Size
		c.free += scanner.Freeable(n, m.pinned)
	}
	m.confirm = c
}

func deleteNodes(nodes []*scanner.Node) tea.Cmd {
	return func() tea.Msg {
		res := deleteDoneMsg{nodes: nodes, errs: make([]error, len(nodes))}
		for i, n := range nodes {
			res.errs[i] = os.RemoveAll(n.Path())
		}
		return res
	}
}

// detach removes n from the tree and subtracts it from its ancestors.
func (m *Model) detach(n *scanner.Node) {
	p := n.Parent
	if p == nil {
		return
	}
	for i, c := range p.Children {
		if c == n {
			p.Children = append(p.Children[:i], p.Children[i+1:]...)
			break
		}
	}
	files, dirs := n.FileCount, n.DirCount
	rec := m.recent[n]
	if n.IsDir {
		dirs++
	} else {
		files = 1
		if n.ModTime >= m.recentSince {
			rec = n.Size
		}
	}
	for a := p; a != nil; a = a.Parent {
		a.Size -= n.Size
		a.FileCount -= files
		a.DirCount -= dirs
		if m.recent != nil && rec > 0 {
			m.recent[a] -= rec
		}
	}
	delete(m.marks, n)
	for i, x := range m.markOrder {
		if x == n {
			m.markOrder = append(m.markOrder[:i], m.markOrder[i+1:]...)
			break
		}
	}
	n.Parent = nil
}

func (m Model) applyDeletes(msg deleteDoneMsg) Model {
	var freed int64
	var ok, failed int
	var firstErr error
	for i, n := range msg.nodes {
		if msg.errs[i] != nil {
			failed++
			if firstErr == nil {
				firstErr = msg.errs[i]
			}
			continue
		}
		ok++
		freed += n.Size
		if n.Parent != nil {
			m.detach(n)
		}
	}
	m.views = nil
	m.topFiles = make(map[*scanner.Node][]*scanner.Node)
	if m.Current != nil {
		// If the current directory (or one of its ancestors) was deleted,
		// go back to the root.
		top := m.Current
		for top.Parent != nil {
			top = top.Parent
		}
		if top != m.Root {
			m.Current = m.Root
			m.Selected = 0
		}
		m.Entries = m.Current.Children
		m.Path = m.Current.Path()
		m.sortEntries()
		m.clampScroll()
	}
	switch {
	case failed == 0:
		m.status = fmt.Sprintf("Supprimé : %d élément(s), %s", ok, display.FormatSize(freed))
	default:
		m.status = fmt.Sprintf("%d supprimé(s), %d en échec : %s — [r] pour rescanner", ok, failed, firstErr)
		m.statusErr = true
	}
	return m
}

// ── Rescan of the current directory ──────────────────────────────────────────

func (m Model) startRescan(target *scanner.Node) (tea.Model, tea.Cmd) {
	if target == nil {
		return m, nil
	}
	if m.rescanning != nil {
		m.status = "Rescan déjà en cours"
		return m, nil
	}
	m.rescanning = target
	opts := m.ScanOpts
	opts.ExpectedHint = int64(target.FileCount) + int64(target.DirCount)
	ch := scanner.Scan(context.Background(), target.Path(), opts)
	m.status = "Rescan de " + textsafe.Clean(target.Path()) + "…"
	return m, tea.Batch(listenRescan(target, ch), spinTick())
}

func listenRescan(target *scanner.Node, ch <-chan scanner.ScanProgress) tea.Cmd {
	return func() tea.Msg {
		for p := range ch {
			if p.Done {
				return rescanMsg{target: target, progress: p}
			}
		}
		return rescanMsg{target: target, progress: scanner.ScanProgress{Done: true, Err: errScanAborted}}
	}
}

func (m Model) handleRescan(msg rescanMsg) (tea.Model, tea.Cmd) {
	m.rescanning = nil
	p := msg.progress
	if p.Err != nil || p.Root == nil {
		m.status, m.statusErr = "Rescan impossible : "+errText(p.Err), true
		return m, nil
	}
	target := msg.target
	if target.Parent == nil && target != m.Root {
		m.status, m.statusErr = "Le dossier a été supprimé entre-temps", true
		return m, nil
	}
	fresh := p.Root
	dSize := fresh.Size - target.Size
	dFiles := fresh.FileCount - target.FileCount
	dDirs := fresh.DirCount - target.DirCount
	oldRecent := m.recent[target]

	// Graft the fresh subtree in place of the old one, keeping the target
	// node itself so the ancestors' Children slices stay valid.
	target.Children = fresh.Children
	for _, c := range target.Children {
		c.Parent = target
	}
	target.Size, target.OwnSize = fresh.Size, fresh.OwnSize
	target.FileCount, target.DirCount = fresh.FileCount, fresh.DirCount
	target.Flags = fresh.Flags | (target.Flags & scanner.FlagMountRoot)
	for a := target.Parent; a != nil; a = a.Parent {
		a.Size += dSize
		a.FileCount += dFiles
		a.DirCount += dDirs
		a.Flags &^= scanner.FlagPartial
		for _, c := range a.Children {
			if c.IsDir && c.Incomplete() {
				a.Flags |= scanner.FlagPartial
				break
			}
		}
	}

	if m.recent == nil {
		m.recent = make(map[*scanner.Node]int64)
	}
	for n, v := range p.Recent {
		if n == fresh {
			n = target
		}
		m.recent[n] = v
	}
	newRecent := p.Recent[fresh]
	if newRecent == 0 {
		delete(m.recent, target)
	}
	for a := target.Parent; a != nil; a = a.Parent {
		m.recent[a] += newRecent - oldRecent
	}
	if m.pinned == nil {
		m.pinned = make(map[*scanner.Node]int64)
	}
	for n, v := range p.Pinned {
		if n == fresh {
			n = target
		}
		m.pinned[n] = v
	}

	// Errors below the target are replaced by the fresh ones.
	prefix := strings.TrimSuffix(target.Path(), "/") + "/"
	var errs []scanner.ScanError
	for _, e := range m.scanErrs {
		if e.Path != target.Path() && !strings.HasPrefix(e.Path, prefix) {
			errs = append(errs, e)
		}
	}
	removed := len(m.scanErrs) - len(errs)
	errs = append(errs, p.Errors...)
	sort.Slice(errs, func(i, j int) bool { return errs[i].Path < errs[j].Path })
	m.scanErrs = errs
	m.errorCount += p.ErrorCount - removed
	if m.errorCount < 0 {
		m.errorCount = 0
	}

	m.views = nil
	m.topFiles = make(map[*scanner.Node][]*scanner.Node)
	// If the user had navigated into the old subtree, its nodes are gone.
	if m.Current != target {
		for a := m.Current; a != nil; a = a.Parent {
			if a == target {
				m.Current = target
				m.Selected = 0
				break
			}
		}
	}
	m.Entries = m.Current.Children
	m.Path = m.Current.Path()
	m.sortEntries()
	m.clampScroll()
	m.status = fmt.Sprintf("Rescan terminé : %s (%s)", display.FormatSize(target.Size), signedSize(dSize))
	return m, nil
}

func errText(err error) string {
	if err == nil {
		return "inconnue"
	}
	return err.Error()
}

// ── Misc ─────────────────────────────────────────────────────────────────────

// copyPath puts path in the terminal's clipboard with OSC 52, which works
// through SSH (and tmux/screen with passthrough).
func (m *Model) copyPath(path string) tea.Cmd {
	seq := osc52.New(path)
	switch {
	case os.Getenv("TMUX") != "":
		seq = seq.Tmux()
	case strings.HasPrefix(os.Getenv("TERM"), "screen"):
		seq = seq.Screen()
	}
	m.status = "Chemin copié (OSC 52) : " + textsafe.Clean(path)
	return func() tea.Msg {
		seq.WriteTo(os.Stderr)
		return nil
	}
}

func (m Model) currentTopFiles() []*scanner.Node {
	if m.Current == nil {
		return nil
	}
	if files, ok := m.topFiles[m.Current]; ok {
		return files
	}
	files := scanner.TopFiles(m.Current, 200)
	if m.topFiles != nil {
		m.topFiles[m.Current] = files
	}
	return files
}

func signedSize(d int64) string {
	switch {
	case d > 0:
		return "+" + display.FormatSize(d)
	case d < 0:
		return "-" + display.FormatSize(-d)
	}
	return "±0"
}
