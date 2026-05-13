package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	var mode string
	var excludeFlag string

	flag.StringVar(&mode, "mode", "global", "Analysis mode: global (all filesystems) or partition (same device only)")
	flag.Parse()

	// Support --exclude or -exclude flags manually (may appear multiple times)
	// For simplicity parse from os.Args directly
	var excludes []string
	if excludeFlag != "" {
		excludes = strings.Split(excludeFlag, ",")
	}
	for i, arg := range os.Args[1:] {
		if (arg == "--exclude" || arg == "-exclude") && i+1 < len(os.Args)-1 {
			excludes = append(excludes, os.Args[i+2])
		}
	}

	path := "."
	if args := flag.Args(); len(args) > 0 {
		path = args[0]
	}

	opts := scanner.ScanOptions{
		SameDevice: mode == "partition",
		Excludes:   excludes,
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch := scanner.Scan(ctx, path, opts)

	m := tui.Model{
		Path:        path,
		Entries:     []scanner.Entry{},
		ScannerChan: ch,
		Scanning:    true,
		CancelScan:  cancel,
		ScanOpts:    opts,
	}

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithInputTTY(), // reconnects stdin to TTY when run via curl | bash
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
