package tui

import (
	"context"
	"fmt"
	"sort"
	"github.com/D1nma/disk_check/internal/scanner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type SortKey int

const (
	SortSize SortKey = iota
	SortName
	SortDate
)

type Model struct {
	Path        string
	Entries     []scanner.Entry
	Selected    int
	Width       int
	Height      int
	Scanning    bool
	ScannerChan <-chan scanner.Entry
	CancelScan  context.CancelFunc // Store current scan cancel function
	History     []string           // Directory stack for navigation
	SortBy      SortKey
	SortReverse bool
}

type NewEntryMsg scanner.Entry

func listenForEntries(entries <-chan scanner.Entry) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-entries
		if !ok {
			return nil // Channel closed
		}
		return NewEntryMsg(e)
	}
}

func (m Model) Init() tea.Cmd {
	if m.ScannerChan != nil {
		return listenForEntries(m.ScannerChan)
	}
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case NewEntryMsg:
		m.Entries = append(m.Entries, scanner.Entry(msg))
		m.sortEntries()
		return m, listenForEntries(m.ScannerChan)

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
			}
		case "down", "j":
			if m.Selected < len(m.Entries)-1 {
				m.Selected++
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
				// Save current path to history before navigating deeper
				m.History = append(m.History, m.Path)
				return m.navigateTo(m.Entries[m.Selected].Path)
			}
		case "backspace", "left", "h":
			if len(m.History) > 0 {
				last := m.History[len(m.History)-1]
				m.History = m.History[:len(m.History)-1]
				return m.navigateTo(last)
			}
		}
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
	}
	return m, nil
}

func (m *Model) sortEntries() {
	sort.Slice(m.Entries, func(i, j int) bool {
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

	// Reset entries and start new scan
	ctx, cancel := context.WithCancel(context.Background())
	m.CancelScan = cancel
	m.Path = newPath
	m.Entries = nil
	m.Selected = 0 // Reset selection
	m.ScannerChan = scanner.Scan(ctx, newPath, 4)
	return m, listenForEntries(m.ScannerChan)
}

func formatSize(size int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	var i int
	fsize := float64(size)
	for fsize >= 1024 && i < len(units)-1 {
		fsize /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", fsize, units[i])
}

func renderBar(width int, percentage float64) string {
	filled := int(float64(width) * percentage)
	if filled > width {
		filled = width
	}
	bar := ""
	for i := 0; i < filled; i++ {
		bar += "#"
	}
	for i := filled; i < width; i++ {
		bar += " "
	}
	return "[" + bar + "]"
}

func (m Model) View() string {
	if len(m.Entries) == 0 {
		return "Scanning..."
	}

	var maxVal int64
	for _, e := range m.Entries {
		if e.Size > maxVal {
			maxVal = e.Size
		}
	}

	var s string
	s += lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf(" Path: %s", m.Path)) + "\n\n"

	for i, entry := range m.Entries {
		cursor := " "
		if m.Selected == i {
			cursor = ">"
		}

		perc := 0.0
		if maxVal > 0 {
			perc = float64(entry.Size) / float64(maxVal)
		}

		bar := renderBar(10, perc)
		sizeStr := formatSize(entry.Size)

		// Adjust padding for sizeStr to keep columns aligned
		s += fmt.Sprintf("%s %10s %s %s\n", cursor, sizeStr, bar, entry.Path)
	}

	return s
}
