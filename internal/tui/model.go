package tui

import (
	"fmt"
	"github.com/D1nma/disk_check/internal/scanner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Model struct {
	Path     string
	Entries  []scanner.Entry
	Selected int
	Width    int
	Height   int
	Scanning bool
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
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
			// TODO: navigate into directory
		case "backspace":
			// TODO: navigate up
		}
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
	}
	return m, nil
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
