package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/D1nma/disk_check/internal/display"
	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/textsafe"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ── Styles ──────────────────────────────────────────────────────────────────

var (
	boldStyle     = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cyanStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	yellowStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	redStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	greenStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	markStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var helpLines = []string{
	"NAVIGATION",
	"  ↑ ↓  k j          déplacer la sélection        PgUp PgDn  g G   page / début / fin",
	"  Entrée → l        ouvrir le dossier            ← h Retour       dossier parent",
	"TRI",
	"  s taille   n nom   t date   c croissance depuis le dernier scan (même touche : inverser)",
	"VUES",
	"  f  plus gros fichiers sous le dossier courant",
	"  e  dossiers illisibles (scan incomplet)",
	"  w  pourquoi df ≠ scan : fichiers supprimés encore ouverts, réservé root, …",
	"  a  activité en direct : qui écrit, quels fichiers grossissent",
	"  ?  cette aide                                  Échap  revenir à la liste",
	"ACTIONS",
	"  espace  marquer / démarquer                    m  revoir les éléments marqués",
	"  dans la vue m : x écrire un plan de nettoyage (script shell à relire), D tout supprimer",
	"  d  supprimer la sélection (confirmation)       r  rescanner le dossier courant",
	"  y  copier le chemin dans le presse-papiers (OSC 52, fonctionne via SSH)",
	"  q  quitter",
	"COLONNES",
	"  taille · % du dossier · barre · Δ depuis le dernier scan · nom · ⚠ incomplet · ★ marqué",
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m Model) width() int {
	if m.Width < 40 {
		return 80
	}
	return m.Width
}

func (m Model) headerLines() []string {
	w := m.width()
	var lines []string

	scanMark := ""
	if m.busy() {
		scanMark = " " + spinnerFrames[m.spin%len(spinnerFrames)]
	}
	sortLabel := map[SortKey]string{SortSize: "taille", SortName: "nom", SortDate: "date", SortGrowth: "croissance"}[m.SortBy]
	if m.SortReverse {
		sortLabel += " ↑"
	} else {
		sortLabel += " ↓"
	}
	modeLabel := "TOUT"
	if m.ScanOpts.SameDevice {
		modeLabel = "PARTITION"
	}
	ver := ""
	if m.Version != "" && m.Version != "dev" {
		ver = "  " + dimStyle.Render(m.Version)
	}
	if m.Imported {
		ver += "  " + dimStyle.Render("distant/importé · lecture seule")
	} else if m.ReadOnly {
		ver += "  " + dimStyle.Render("lecture seule")
	}
	header := fmt.Sprintf("DISK EXPLORER  %s  %s · %s%s", textsafe.Clean(m.hostPrefix+m.Path), modeLabel, sortLabel, scanMark)
	lines = append(lines, boldStyle.Render(header)+ver)

	if m.diskTotal > 0 {
		pct := int(m.diskUsed * 100 / m.diskTotal)
		bar := cyanStyle.Render(renderBar(m.diskUsed, m.diskTotal, 20))
		diskLine := fmt.Sprintf("%s %d%%  %s / %s  · libre %s",
			bar, pct, formatSize(m.diskUsed), formatSize(m.diskTotal), formatSize(m.diskAvail))
		if m.diskAvail < m.diskTotal/10 {
			diskLine = yellowStyle.Render(diskLine)
		}
		if m.fillText != "" {
			diskLine += "  " + yellowStyle.Render(m.fillText)
		}
		lines = append(lines, diskLine)
	}

	if m.State == StateBrowsing && m.Current != nil {
		var parts []string
		parts = append(parts, formatSize(m.Current.Size))
		if m.Current.Parent != nil && m.Current.Parent.Size > 0 {
			parts = append(parts, fmt.Sprintf("%d%% du parent", m.Current.Size*100/m.Current.Parent.Size))
		}
		if m.diskTotal > 0 {
			parts = append(parts, fmt.Sprintf("%d%% du disque", m.Current.Size*100/m.diskTotal))
		}
		parts = append(parts, fmt.Sprintf("%d fichiers", m.Current.FileCount))
		parts = append(parts, fmt.Sprintf("%d dossiers", m.Current.DirCount))
		if r := m.recent[m.Current]; r > 0 {
			parts = append(parts, "modifié < 24 h : "+formatSize(r))
		}
		here := dimStyle.Render("  " + strings.Join(parts, "  "))
		if m.errorCount > 0 {
			here += "  " + yellowStyle.Render(fmt.Sprintf("⚠ %d illisible(s) [e]", m.errorCount))
		}
		lines = append(lines, here)
	}
	lines = append(lines, dimStyle.Render(strings.Repeat("─", w)))
	return lines
}

func (m Model) footerLines() []string {
	w := m.width()
	lines := []string{dimStyle.Render(strings.Repeat("─", w))}

	if m.State == StateBrowsing {
		if d := m.detailLine(); d != "" {
			lines = append(lines, d)
		}
		if len(m.marks) > 0 {
			total, free := m.markTotals()
			lines = append(lines, markStyle.Render(fmt.Sprintf("★ %d marqué(s) — %s, libère ~%s  · [m] revoir / plan", len(m.markedRoots()), formatSize(total), formatSize(free))))
		}
	}

	var keys string
	switch {
	case m.State == StateScanning:
		keys = "[q] quitter"
	case m.mode == viewList:
		keys = "[?] aide  [⏎] ouvrir  [←] retour  [s/n/t/c] tri  [espace] marquer  [d] suppr  [f] gros fichiers  [w] df≠du  [a] activité  [r] rescan  [y] copier  [q] quitter"
	case m.mode == viewMarks:
		keys = "[x] écrire le plan  [D] tout supprimer  [espace] démarquer  [⏎] aller à  [Échap] retour"
	case m.mode == viewTopFiles:
		keys = "[⏎] aller au fichier  [espace] marquer  [d] supprimer  [y] copier  [Échap] retour"
	case m.mode == viewErrors:
		keys = "[⏎] aller au dossier  [y] copier  [Échap] retour"
	default:
		keys = "[Échap] retour  [q] quitter"
	}
	lines = append(lines, dimStyle.Render(keys))

	if m.confirm != nil {
		c := m.confirm
		what := textsafe.Clean(c.nodes[0].Path())
		if len(c.nodes) > 1 {
			what = fmt.Sprintf("%d éléments", len(c.nodes))
		}
		lines = append(lines, redStyle.Bold(true).Render(fmt.Sprintf("Supprimer définitivement %s (%s, libère ~%s) ? [o]ui / [N]on", what, formatSize(c.total), formatSize(c.free))))
	} else if m.status != "" {
		if m.statusErr {
			lines = append(lines, redStyle.Render("  "+m.status))
		} else {
			lines = append(lines, greenStyle.Render("  "+m.status))
		}
	}

	switch m.updateState {
	case updateDownloading:
		lines = append(lines, yellowStyle.Render(fmt.Sprintf("  ⬇ Mise à jour %s : téléchargement en cours…", m.updateTag)))
	case updateReady:
		lines = append(lines, yellowStyle.Render(fmt.Sprintf("  Mise à jour %s prête — [u] installer et redémarrer  ·  [i] ignorer cette session", m.updateTag)))
	case updateFailed:
		lines = append(lines, yellowStyle.Render(fmt.Sprintf("  Mise à jour %s indisponible : %s", m.updateTag, m.updateErr)))
	case updateNotice:
		lines = append(lines, yellowStyle.Render(fmt.Sprintf("  Version %s disponible (binaire non modifiable ici : utilisez votre gestionnaire de paquets ou --update avec les droits) · [i] masquer", m.updateTag)))
	}
	return lines
}

// detailLine describes the selected entry: what deleting it frees, recent
// activity, growth, and what the path is (docker container, cache…).
func (m Model) detailLine() string {
	var e *scanner.Node
	switch m.mode {
	case viewList:
		if len(m.Entries) > 0 && m.Selected < len(m.Entries) {
			e = m.Entries[m.Selected]
		}
	case viewTopFiles:
		if files := m.currentTopFiles(); m.overSel < len(files) {
			e = files[m.overSel]
		}
	case viewMarks:
		if marked := m.markedRoots(); m.overSel < len(marked) {
			e = marked[m.overSel]
		}
	}
	if e == nil {
		return ""
	}
	var parts []string
	if f := scanner.Freeable(e, m.pinned); f != e.Size {
		parts = append(parts, "libère "+formatSize(f)+" (liens durs ailleurs)")
	}
	if e.Flags&scanner.FlagHardlink != 0 {
		if e.Size == 0 {
			parts = append(parts, "lien dur (taille comptée sur un autre lien)")
		} else {
			parts = append(parts, "lien dur")
		}
	}
	if e.IsDir {
		if r := m.recent[e]; r > 0 {
			parts = append(parts, "modifié < 24 h : "+formatSize(r))
		}
	} else if e.ModTime >= m.recentSince && m.recentSince > 0 {
		parts = append(parts, "modifié < 24 h")
	}
	if m.Prev != nil {
		if d, exact := m.Prev.Delta(e); d != 0 {
			s := "Δ " + signedSize(d) + " depuis le " + m.Prev.Time.Format("02/01 15:04")
			if !exact {
				s += " (nouveau)"
			}
			parts = append(parts, s)
		}
	}
	switch {
	case e.Flags&scanner.FlagUnreadable != 0:
		parts = append(parts, "⚠ illisible : taille minimale")
	case e.Flags&scanner.FlagPartial != 0:
		parts = append(parts, "⚠ contient des dossiers illisibles")
	}
	if e.Flags&scanner.FlagMountRoot != 0 {
		parts = append(parts, "point de montage")
	}
	line := strings.Join(parts, " · ")
	if m.Hooks.Annotate != nil {
		if label, cleanup, ok := m.Hooks.Annotate(e.Path(), e.IsDir); ok {
			a := textsafe.Clean(label)
			if cleanup != "" {
				a += " → " + textsafe.Clean(cleanup)
			}
			if line != "" {
				line = a + " · " + line
			} else {
				line = a
			}
		}
	}
	if line == "" {
		return ""
	}
	return cyanStyle.Render("  " + line)
}

func (m Model) listHeight() int {
	h := m.Height - len(m.headerLines()) - len(m.footerLines())
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) View() string {
	w := m.width()
	header := m.headerLines()
	footer := m.footerLines()
	visible := m.Height - len(header) - len(footer)
	if visible < 1 {
		visible = 1
	}

	var body []string
	switch {
	case m.State == StateScanning:
		body = m.progressLines(w)
	case m.mode == viewList:
		body = m.listLines(visible)
	default:
		body = m.overlayLines(visible)
	}
	for len(body) < visible {
		body = append(body, "")
	}
	if len(body) > visible {
		body = body[:visible]
	}

	all := make([]string, 0, len(header)+len(body)+len(footer))
	all = append(all, header...)
	all = append(all, body...)
	all = append(all, footer...)
	for i, l := range all {
		all[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(all, "\n")
}

func (m Model) progressLines(w int) []string {
	p := m.Progress
	lines := []string{"", fmt.Sprintf("  Analyse en cours : %s", textsafe.Clean(m.hostPrefix+m.Path)), ""}
	seen := int64(p.Files + p.Dirs)
	if p.Expected > 0 {
		frac := float64(seen) / float64(p.Expected)
		if frac > 0.99 {
			frac = 0.99 // never claim 100 % before the end
		}
		eta := ""
		if frac > 0.02 && p.Elapsed > time.Second {
			left := time.Duration(float64(p.Elapsed) * (1 - frac) / frac)
			eta = "  ~" + humanDuration(left) + " restantes"
		}
		bar := cyanStyle.Render(renderBar(int64(frac*1000), 1000, 30))
		lines = append(lines, fmt.Sprintf("  %s %3.0f %%%s  (≈ %d entrées attendues)", bar, frac*100, eta, p.Expected), "")
	}
	lines = append(lines,
		fmt.Sprintf("  Fichiers : %d", p.Files),
		fmt.Sprintf("  Dossiers : %d", p.Dirs),
		fmt.Sprintf("  Taille   : %s", formatSize(p.Size)),
		fmt.Sprintf("  Écoulé   : %s", humanDuration(p.Elapsed)),
	)
	if p.ErrorCount > 0 {
		lines = append(lines, yellowStyle.Render(fmt.Sprintf("  Illisibles : %d", p.ErrorCount)))
	}
	lines = append(lines, "")
	if p.Current != "" {
		lines = append(lines, dimStyle.Render("  En cours : "+textsafe.Clean(p.Current)))
	}
	return lines
}

func (m Model) listLines(visible int) []string {
	if len(m.Entries) == 0 {
		return []string{dimStyle.Render("  (vide)")}
	}
	var dirSize int64
	if m.Current != nil {
		dirSize = m.Current.Size
	}
	var lines []string
	for i := m.Offset; i < len(m.Entries) && i < m.Offset+visible; i++ {
		e := m.Entries[i]
		cursor := "  "
		if i == m.Selected {
			cursor = "> "
		}
		mark := "  "
		if m.marks[e] {
			mark = "★ "
		}
		bar := renderBar(e.Size, m.maxSize, 10)
		pctStr := "  0.0%"
		if dirSize > 0 {
			pctStr = fmt.Sprintf("%5.1f%%", float64(e.Size)*100/float64(dirSize))
		}
		delta := ""
		if m.Prev != nil {
			if d, exact := m.Prev.Delta(e); d != 0 {
				delta = signedSize(d)
				if !exact {
					delta = "+nouveau"
				}
			}
			delta = fmt.Sprintf("%11s  ", delta)
		}
		name := textsafe.Clean(e.Name)
		if e.IsDir {
			name += "/"
		}
		flags := ""
		if e.Incomplete() {
			flags += " ⚠"
		}
		label := ""
		if m.Hooks.Annotate != nil {
			if l, _, ok := m.Hooks.Annotate(e.Path(), e.IsDir); ok && l != "" {
				label = "  · " + textsafe.Clean(l)
			}
		}
		if i == m.Selected {
			line := fmt.Sprintf("%s%s%10s  %s  %s  %s%s%s%s", cursor, mark, formatSize(e.Size), pctStr, bar, delta, name, flags, label)
			lines = append(lines, selectedStyle.Render(line))
			continue
		}
		deltaStyled := delta
		if strings.Contains(delta, "+") {
			deltaStyled = yellowStyle.Render(delta)
		} else if strings.Contains(delta, "-") {
			deltaStyled = greenStyle.Render(delta)
		}
		markStyled := mark
		if m.marks[e] {
			markStyled = markStyle.Render(mark)
		}
		flagStyled := flags
		if flags != "" {
			flagStyled = yellowStyle.Render(flags)
		}
		lines = append(lines, fmt.Sprintf("%s%s%10s  %s  %s  %s%s%s%s", cursor, markStyled, formatSize(e.Size), pctStr,
			cyanStyle.Render(bar), deltaStyled, name, flagStyled, dimStyle.Render(label)))
	}
	return lines
}

func (m Model) overlayLines(visible int) []string {
	var title string
	var items []string
	switch m.mode {
	case viewHelp:
		title = "AIDE"
		items = helpLines
	case viewTopFiles:
		files := m.currentTopFiles()
		title = fmt.Sprintf("PLUS GROS FICHIERS sous %s (%d)", textsafe.Clean(m.Path), len(files))
		for _, f := range files {
			mark := "  "
			if m.marks[f] {
				mark = "★ "
			}
			items = append(items, fmt.Sprintf("%s%10s  %s", mark, formatSize(f.Size), textsafe.Clean(relativeTo(m.Current, f))))
		}
		if len(files) == 0 {
			items = []string{"  (aucun fichier)"}
		}
	case viewErrors:
		title = fmt.Sprintf("DOSSIERS ILLISIBLES (%d) — les tailles au-dessus sont sous-estimées ; relancer avec sudo pour tout voir", m.errorCount)
		for _, e := range m.scanErrs {
			items = append(items, fmt.Sprintf("  %s : %s", textsafe.Clean(e.Path), e.Err))
		}
		if m.errorCount > len(m.scanErrs) {
			items = append(items, fmt.Sprintf("  … et %d autre(s) non listé(s)", m.errorCount-len(m.scanErrs)))
		}
	case viewMarks:
		marked := m.markedRoots()
		total, free := m.markTotals()
		title = fmt.Sprintf("PLAN DE NETTOYAGE — %d élément(s), %s, libère ~%s", len(marked), formatSize(total), formatSize(free))
		for _, n := range marked {
			line := fmt.Sprintf("  %10s  %s", formatSize(n.Size), textsafe.Clean(n.Path()))
			if m.Hooks.Annotate != nil {
				if _, cleanup, ok := m.Hooks.Annotate(n.Path(), n.IsDir); ok && cleanup != "" {
					line += "  → " + textsafe.Clean(cleanup)
				}
			}
			items = append(items, line)
		}
		if len(marked) == 0 {
			items = []string{"  (rien de marqué : [espace] sur une entrée pour la marquer)"}
		}
	case viewWhy:
		title = "POURQUOI DF ≠ SCAN — " + textsafe.Clean(m.Path)
		items = m.whyLines
		if items == nil {
			items = []string{"  analyse en cours…"}
		}
	case viewActivity:
		title = "ACTIVITÉ EN DIRECT (rafraîchie toutes les 2 s) — " + textsafe.Clean(m.Path)
		items = m.actLines
		if items == nil {
			items = []string{"  première mesure…"}
		}
	}
	lines := []string{boldStyle.Render(title)}
	rows := visible - 1
	selectable := m.mode == viewTopFiles || m.mode == viewErrors || m.mode == viewMarks
	for i := m.overOff; i < len(items) && i < m.overOff+rows; i++ {
		l := items[i]
		if selectable && i == m.overSel {
			l = selectedStyle.Render(">" + strings.TrimPrefix(l, " "))
		}
		lines = append(lines, l)
	}
	return lines
}

func relativeTo(base, n *scanner.Node) string {
	var parts []string
	for c := n; c != nil && c != base; c = c.Parent {
		parts = append(parts, c.Name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/")
}

// ── Formatting ────────────────────────────────────────────────────────────────

func formatSize(size int64) string { return display.FormatSize(size) }

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1f s", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%d min %02d s", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
	}
}

// renderBar returns a UTF-8 bar of `width` chars, `filled/total` proportion filled.
func renderBar(filled, total, width int64) string {
	if total <= 0 {
		total = 1
	}
	n := filled * width / total
	if n > width {
		n = width
	}
	if n < 0 {
		n = 0
	}
	return strings.Repeat("█", int(n)) + strings.Repeat("░", int(width-n))
}
