package main

import (
	"context"
	"fmt"
	"os"

	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	path := "."
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	ctx, cancel := context.WithCancel(context.Background())
	entriesChan := scanner.Scan(ctx, path, 4)

	m := tui.Model{
		Path:        path,
		Entries:     []scanner.Entry{},
		ScannerChan: entriesChan,
		Scanning:    true,
		CancelScan:  cancel,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running program: %v\n", err)
		os.Exit(1)
	}
}
