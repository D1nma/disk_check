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

var version = "dev"

func main() {
	var mode string
	var excludeFlag string

	flag.StringVar(&mode, "mode", "global", "Analysis mode: global (all filesystems) or partition (same device only)")
	flag.Parse()

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
		Version:     version,
		Entries:     []scanner.Entry{},
		ScannerChan: ch,
		Scanning:    true,
		CancelScan:  cancel,
		ScanOpts:    opts,
	}

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithInputTTY(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
