package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/D1nma/disk_check/internal/scanner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Sort ────────────────────────────────────────────────────────────────────

type SortKey int

const (
	SortSize SortKey = iota
	SortName
	SortDate
)

// ── Styles ──────────────────────────────────────────────────────────────────

var (
	boldStyle     = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cyanStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	yellowStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

// ── Messages ─────────────────────────────────────────────────────────────────

// generation is embedded in scan messages to discard stale results after navigation.
type entryMsg struct {
	gen   int
	entry scanner.Entry
}

type scanDoneMsg struct{ gen int }

type diskInfoMsg struct {
	Total int64
	Used  int64
	Avail int64
}

// ── Model ────────────────────────────────────────────────────────────────────

type Model struct {
	Path        string
	Version     string
	Entries     []scanner.Entry
	Selected    int
	Offset      int // scroll offset
	Width       int
	Height      int
	Scanning    bool
	ScannerChan <-chan scanner.Entry
	CancelScan  context.CancelFunc
	History     []string
	SortBy      SortKey
	SortReverse bool
	ScanOpts    scanner.ScanOptions

	// disk usage header
	diskTotal int64
	diskUsed  int64
	diskAvail int64

	// generation counter — incremented on each navigation to discard stale messages
	generation int
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{fetchDiskInfo(m.Path)}
	if m.ScannerChan != nil {
		cmds = append(cmds, listenForEntry(m.ScannerChan, m.generation))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case diskInfoMsg:
		m.diskTotal = msg.Total
		m.diskUsed = msg.Used
		m.diskAvail = msg.Avail

	case entryMsg:
		if msg.gen != m.generation {
			break // stale — discard
		}
		m.Entries = append(m.Entries, msg.entry)
		m.sortEntries()
		return m, listenForEntry(m.ScannerChan, m.generation)

	case scanDoneMsg:
		if msg.gen != m.generation {
			break // stale
		}
		m.Scanning = false
		m.ScannerChan = nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.CancelScan != nil {
				m.CancelScan()
			}
			return m, tea.Quit

		case "up", "k":
			if m.Selected > 0 {
				m.Selected--
				m.clampScroll()
			}
		case "down", "j":
			if m.Selected < len(m.Entries)-1 {
				m.Selected++
				m.clampScroll()
			}

		case "s":
			if m.SortBy == SortSize {
				m.SortReverse = !m.SortReverse
			} else {
				m.SortBy = SortSize
				m.SortReverse = false
			}
			m.sortEntries()
		case "n":
			if m.SortBy == SortName {
				m.SortReverse = !m.SortReverse
			} else {
				m.SortBy = SortName
				m.SortReverse = false
			}
			m.sortEntries()
		case "t":
			if m.SortBy == SortDate {
				m.SortReverse = !m.SortReverse
			} else {
				m.SortBy = SortDate
				m.SortReverse = false
			}
			m.sortEntries()

		case "enter":
			if len(m.Entries) > 0 && m.Entries[m.Selected].IsDir {
				m.History = append(m.History, m.Path)
				return m.navigateTo(m.Entries[m.Selected].Path)
			}

		case "backspace", "left", "h":
			if len(m.History) > 0 {
				prev := m.History[len(m.History)-1]
				m.History = m.History[:len(m.History)-1]
				return m.navigateTo(prev)
			}
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
	}
	return m, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func (m *Model) clampScroll() {
	visible := m.listHeight()
	if m.Selected < m.Offset {
		m.Offset = m.Selected
	} else if m.Selected >= m.Offset+visible {
		m.Offset = m.Selected - visible + 1
	}
}

func (m Model) listHeight() int {
	h := m.Height - 5 // header(1) + disk(1) + sep(1) + sep(1) + footer(1)
	if h < 1 {
		h = 1
	}
	return h
}

func (m *Model) sortEntries() {
	sort.SliceStable(m.Entries, func(i, j int) bool {
		var res bool
		switch m.SortBy {
		case SortSize:
			res = m.Entries[i].Size > m.Entries[j].Size
		case SortName:
			res = m.Entries[i].Path < m.Entries[j].Path
		case SortDate:
			res = m.Entries[i].ModTime.After(m.Entries[j].ModTime)
		}
		if m.SortReverse {
			return !res
		}
		return res
	})
}

func (m Model) navigateTo(newPath string) (tea.Model, tea.Cmd) {
	if m.CancelScan != nil {
		m.CancelScan()
	}
	m.generation++
	ctx, cancel := context.WithCancel(context.Background())
	m.CancelScan = cancel
	m.Path = newPath
	m.Entries = nil
	m.Selected = 0
	m.Offset = 0
	m.Scanning = true
	ch := scanner.Scan(ctx, newPath, m.ScanOpts)
	m.ScannerChan = ch
	return m, tea.Batch(
		fetchDiskInfo(newPath),
		listenForEntry(ch, m.generation),
	)
}

// ── Commands ─────────────────────────────────────────────────────────────────

func listenForEntry(ch <-chan scanner.Entry, gen int) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return scanDoneMsg{gen: gen}
		}
		return entryMsg{gen: gen, entry: e}
	}
}

func fetchDiskInfo(path string) tea.Cmd {
	return func() tea.Msg {
		var st syscall.Statfs_t
		if err := syscall.Statfs(path, &st); err != nil {
			return diskInfoMsg{}
		}
		bs := int64(st.Bsize)
		return diskInfoMsg{
			Total: int64(st.Blocks) * bs,
			Used:  (int64(st.Blocks) - int64(st.Bfree)) * bs,
			Avail: int64(st.Bavail) * bs,
		}
	}
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	w := m.Width
	if w < 40 {
		w = 80
	}
	sep := dimStyle.Render(strings.Repeat("─", w))

	var b strings.Builder

	// Line 1: title
	scanMark := ""
	if m.Scanning {
		scanMark = " ⠋"
	}
	sortLabel := map[SortKey]string{SortSize: "size", SortName: "name", SortDate: "date"}[m.SortBy]
	if m.SortReverse {
		sortLabel += " ↑"
	} else {
		sortLabel += " ↓"
	}
	modeLabel := "ALL"
	if m.ScanOpts.SameDevice {
		modeLabel = "PARTITION"
	}
	ver := ""
	if m.Version != "" && m.Version != "dev" {
		ver = "  " + dimStyle.Render(m.Version)
	}
	header := fmt.Sprintf("DISK EXPLORER  %s  %s · %s%s", m.Path, modeLabel, sortLabel, scanMark) + ver
	b.WriteString(boldStyle.Render(header) + "\n")

	// Line 2: disk usage bar
	if m.diskTotal > 0 {
		pct := int(m.diskUsed * 100 / m.diskTotal)
		bar := cyanStyle.Render(renderBar(m.diskUsed, m.diskTotal, 20))
		diskLine := fmt.Sprintf("%s %d%%  %s / %s",
			bar, pct, formatSize(m.diskUsed), formatSize(m.diskTotal))
		if m.diskAvail < m.diskTotal/10 {
			diskLine = yellowStyle.Render(diskLine)
		}
		b.WriteString(diskLine + "\n")
	}

	b.WriteString(sep + "\n")

	// Entry list
	visible := m.listHeight()
	var maxSize int64
	for _, e := range m.Entries {
		if e.Size > maxSize {
			maxSize = e.Size
		}
	}

	if len(m.Entries) == 0 {
		if m.Scanning {
			b.WriteString(dimStyle.Render("  Analyse en cours...") + "\n")
		} else {
			b.WriteString(dimStyle.Render("  (vide)") + "\n")
		}
	}

	for i := m.Offset; i < len(m.Entries) && i < m.Offset+visible; i++ {
		e := m.Entries[i]
		cursor := "  "
		if i == m.Selected {
			cursor = "> "
		}
		bar := cyanStyle.Render(renderBar(e.Size, maxSize, 10))
		name := filepath.Base(e.Path)
		if e.IsDir {
			name += "/"
		}
		line := fmt.Sprintf("%s%10s  %s  %s", cursor, formatSize(e.Size), bar, name)
		if i == m.Selected {
			b.WriteString(selectedStyle.Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}

	// Pad remaining lines so footer stays at the bottom
	rendered := m.Offset + visible
	if rendered > len(m.Entries) {
		rendered = len(m.Entries)
	}
	for i := rendered - m.Offset; i < visible; i++ {
		b.WriteString("\n")
	}

	b.WriteString(sep + "\n")

	// Footer
	footer := "[↑↓/jk] nav  [Enter] cd  [←/h] retour  [s]ize [n]ame [t]ime  [q]uit"
	b.WriteString(dimStyle.Render(footer))

	return b.String()
}

// ── Formatting ────────────────────────────────────────────────────────────────

func formatSize(size int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f := float64(size)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
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
