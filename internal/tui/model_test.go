package tui

import (
	"testing"

	"github.com/D1nma/disk_check/internal/scanner"
	tea "github.com/charmbracelet/bubbletea"
)

func TestModelSortEntriesBySize(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Name: "small", Size: 100},
			{Name: "large", Size: 1000},
			{Name: "medium", Size: 500},
		},
		SortBy:      SortSize,
		SortReverse: false,
	}
	m.sortEntries()
	if m.Entries[0].Name != "large" {
		t.Errorf("Expected large first, got %s", m.Entries[0].Name)
	}
	if m.Entries[2].Name != "small" {
		t.Errorf("Expected small last, got %s", m.Entries[2].Name)
	}
}

func TestModelSortEntriesByName(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Name: "b", Size: 100},
			{Name: "a", Size: 1000},
			{Name: "c", Size: 500},
		},
		SortBy:      SortName,
		SortReverse: false,
	}
	m.sortEntries()
	if m.Entries[0].Name != "a" {
		t.Errorf("Expected a first, got %s", m.Entries[0].Name)
	}
	if m.Entries[2].Name != "c" {
		t.Errorf("Expected c last, got %s", m.Entries[2].Name)
	}
}

func TestModelSortEntriesByReverseSize(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Name: "small", Size: 100},
			{Name: "large", Size: 1000},
			{Name: "medium", Size: 500},
		},
		SortBy:      SortSize,
		SortReverse: true,
	}
	m.sortEntries()
	if m.Entries[0].Name != "small" {
		t.Errorf("Expected small first, got %s", m.Entries[0].Name)
	}
	if m.Entries[2].Name != "large" {
		t.Errorf("Expected large last, got %s", m.Entries[2].Name)
	}
}

func TestModelNavigation(t *testing.T) {
	root := &scanner.Node{Name: "/", IsDir: true}
	child := &scanner.Node{Name: "child", IsDir: true, Parent: root}
	root.Children = []*scanner.Node{child}

	m := Model{
		State:   StateBrowsing,
		Current: root,
		Entries: root.Children,
		Path:    "/",
	}

	msg := tea.KeyMsg{Type: tea.KeyEnter}
	newModel, cmd := m.Update(msg)
	m = newModel.(Model)

	if cmd != nil {
		t.Error("Expected nil command for instant navigation")
	}
	if m.Current != child {
		t.Errorf("Expected current node to be child, got %s", m.Current.Path())
	}
	if m.Path != "/child" {
		t.Errorf("Expected path to be /child, got %s", m.Path)
	}

	msg = tea.KeyMsg{Type: tea.KeyBackspace}
	newModel, cmd = m.Update(msg)
	m = newModel.(Model)

	if cmd != nil {
		t.Error("Expected nil command for instant navigation")
	}
	if m.Current != root {
		t.Errorf("Expected current node to be root, got %s", m.Current.Path())
	}
	if m.Path != "/" {
		t.Errorf("Expected path to be /, got %s", m.Path)
	}
}
