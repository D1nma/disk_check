package tui

import (
	"testing"
	"github.com/D1nma/disk_check/internal/scanner"
	tea "github.com/charmbracelet/bubbletea"
)

func TestModelSortEntriesBySize(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Path: "small", Size: 100},
			{Path: "large", Size: 1000},
			{Path: "medium", Size: 500},
		},
		SortBy:      SortSize,
		SortReverse: false,
	}
	m.sortEntries()
	if m.Entries[0].Path != "large" {
		t.Errorf("Expected large first, got %s", m.Entries[0].Path)
	}
	if m.Entries[2].Path != "small" {
		t.Errorf("Expected small last, got %s", m.Entries[2].Path)
	}
}

func TestModelSortEntriesByName(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Path: "b", Size: 100},
			{Path: "a", Size: 1000},
			{Path: "c", Size: 500},
		},
		SortBy:      SortName,
		SortReverse: false,
	}
	m.sortEntries()
	if m.Entries[0].Path != "a" {
		t.Errorf("Expected a first, got %s", m.Entries[0].Path)
	}
	if m.Entries[2].Path != "c" {
		t.Errorf("Expected c last, got %s", m.Entries[2].Path)
	}
}

func TestModelSortEntriesByReverseSize(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Path: "small", Size: 100},
			{Path: "large", Size: 1000},
			{Path: "medium", Size: 500},
		},
		SortBy:      SortSize,
		SortReverse: true,
	}
	m.sortEntries()
	if m.Entries[0].Path != "small" {
		t.Errorf("Expected small first, got %s", m.Entries[0].Path)
	}
	if m.Entries[2].Path != "large" {
		t.Errorf("Expected large last, got %s", m.Entries[2].Path)
	}
}

func TestModelNavigation(t *testing.T) {
	root := &scanner.Node{Path: "/", IsDir: true}
	child := &scanner.Node{Path: "/child", IsDir: true, Parent: root}
	root.Children = []*scanner.Node{child}

	m := Model{
		State:   StateBrowsing,
		Current: root,
		Entries: root.Children,
		Path:    "/",
	}

	// Navigate into child
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	newModel, cmd := m.Update(msg)
	m = newModel.(Model)

	if cmd != nil {
		t.Error("Expected nil command for instant navigation")
	}
	if m.Current != child {
		t.Errorf("Expected current node to be child, got %s", m.Current.Path)
	}
	if m.Path != "/child" {
		t.Errorf("Expected path to be /child, got %s", m.Path)
	}

	// Navigate back
	msg = tea.KeyMsg{Type: tea.KeyBackspace}
	newModel, cmd = m.Update(msg)
	m = newModel.(Model)

	if cmd != nil {
		t.Error("Expected nil command for instant navigation")
	}
	if m.Current != root {
		t.Errorf("Expected current node to be root, got %s", m.Current.Path)
	}
	if m.Path != "/" {
		t.Errorf("Expected path to be /, got %s", m.Path)
	}
}
