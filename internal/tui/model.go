package tui

import (
	"context"
	"fmt"
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
	ScannerChan chan scanner.Entry
	CancelScan  context.CancelFunc // Store current scan cancel function
	History     []string           // Directory stack for navigation
	SortBy      SortKey
	SortReverse bool
}

type NewEntryMsg scanner.Entry

func listenForEntries(entries chan scanner.Entry) tea.Cmd {
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

func (m Model) View() string {
	if len(m.Entries) == 0 {
		return "Scanning..."
	}

	var s string
	s += lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf(" Path: %s", m.Path)) + "\n\n"

	for i, entry := range m.Entries {
		cursor := " "
		if m.Selected == i {
			cursor = ">"
		}
		s += fmt.Sprintf("%s %s\n", cursor, entry.Path)
	}

	return s
}
