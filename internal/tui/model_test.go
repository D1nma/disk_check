package tui

import (
	"strings"
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

func TestSortReverseEqualValuesDeterministic(t *testing.T) {
	m := Model{
		Entries: []*scanner.Node{
			{Name: "z", Size: 10},
			{Name: "a", Size: 10},
			{Name: "m", Size: 10},
		},
		SortBy:      SortSize,
		SortReverse: true,
	}
	m.sortEntries()
	for i, want := range []string{"z", "m", "a"} {
		if m.Entries[i].Name != want {
			t.Fatalf("entry %d = %q, want %q", i, m.Entries[i].Name, want)
		}
	}
}

func TestNavigationRestoresCachedDirectoryView(t *testing.T) {
	root := &scanner.Node{Name: "/data", IsDir: true}
	child := &scanner.Node{Name: "sub", IsDir: true, Size: 100, Parent: root}
	root.Children = []*scanner.Node{{Name: "small", Size: 1, Parent: root}, child}
	m := Model{State: StateBrowsing, Current: root, Entries: root.Children, Height: 24}
	m.sortEntries()
	if m.maxSize != 100 {
		t.Fatalf("maxSize = %d, want 100", m.maxSize)
	}
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = model.(Model)
	if m.maxSize != 100 || m.Current != root || m.Entries[m.Selected] != child {
		t.Fatalf("parent view not restored: max=%d selected=%d", m.maxSize, m.Selected)
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

func TestViewHereLineOmitsParentAtRoot(t *testing.T) {
	root := &scanner.Node{Name: "/data", Size: 1000, FileCount: 3, DirCount: 1, IsDir: true}
	child := &scanner.Node{Name: "a", Size: 400, IsDir: true, Parent: root}
	root.Children = []*scanner.Node{child}
	m := Model{
		State:     StateBrowsing,
		Path:      "/data",
		Current:   root,
		Entries:   root.Children,
		diskTotal: 10000,
		Width:     80,
		Height:    24,
	}
	v := m.View()
	if !strings.Contains(v, "/data") {
		t.Errorf("missing current path in view:\n%s", v)
	}
	if strings.Contains(v, "of parent") {
		t.Errorf("root should omit parent percent:\n%s", v)
	}
	if !strings.Contains(v, "of disk") {
		t.Errorf("missing disk percent:\n%s", v)
	}
}

func TestViewHereLineIncludesParentWhenNested(t *testing.T) {
	root := &scanner.Node{Name: "/data", Size: 1000, IsDir: true}
	child := &scanner.Node{Name: "a", Size: 400, FileCount: 1, DirCount: 0, IsDir: true, Parent: root}
	root.Children = []*scanner.Node{child}
	m := Model{
		State:     StateBrowsing,
		Path:      "/data/a",
		Current:   child,
		Entries:   child.Children,
		diskTotal: 10000,
		Width:     80,
		Height:    24,
	}
	v := m.View()
	if !strings.Contains(v, "of parent") {
		t.Errorf("nested dir should show parent percent:\n%s", v)
	}
}
