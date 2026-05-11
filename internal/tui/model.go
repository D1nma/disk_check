package tui

import (
    "github.com/D1nma/disk_check/internal/scanner"
    tea "github.com/charmbracelet/bubbletea"
)

type Model struct {
    Path    string
    Entries []scanner.Entry
    Ready   bool
}

type ScanUpdateMsg []scanner.Entry

func (m Model) Init() tea.Cmd {
    return nil
}

// Add empty Update and View methods to satisfy tea.Model interface
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    return m, nil
}

func (m Model) View() string {
    return "Disk Explorer\n"
}
