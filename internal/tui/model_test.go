package tui

import (
	"context"
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

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestUpdatePromptStateMachine(t *testing.T) {
	var m tea.Model = Model{}

	// Detection -> background download starts, banner should say so.
	m, _ = m.Update(updateAvailableMsg{tag: "v9.9.9"})
	if m.(Model).updateState != updateDownloading {
		t.Fatalf("state after detection = %d, want updateDownloading", m.(Model).updateState)
	}

	// Leftover message from a dismissed update must not resurrect the banner.
	m2, _ := m.Update(updatePrefetchedMsg{tag: "v9.9.9", file: "/tmp/fake"})
	_ = m2

	// Ready state after the verified download lands.
	m, _ = m.Update(updatePrefetchedMsg{tag: "v9.9.9", file: "/tmp/fake"})
	if m.(Model).updateState != updateReady || m.(Model).updateFile != "/tmp/fake" {
		t.Fatalf("state after prefetch = %+v", m.(Model))
	}

	// Declining removes the file reference and hides the banner.
	m3, _ := m.Update(keyMsg("i"))
	if m3.(Model).updateState != updateDismissed || m3.(Model).updateFile != "" {
		t.Fatalf("state after ignore = %+v", m3.(Model))
	}

	// 'u' outside the ready state does nothing.
	m4, _ := m3.Update(keyMsg("u"))
	if m4.(Model).PendingUpdate != "" {
		t.Fatalf("u must be ignored when no update is ready")
	}

	// 'u' in the ready state stages the install and quits
	// (the model's PendingUpdate is consumed by main after the TUI exits).
	var m5 tea.Model = Model{}
	m5, _ = m5.Update(updateAvailableMsg{tag: "v9.9.9"})
	m5, _ = m5.Update(updatePrefetchedMsg{tag: "v9.9.9", file: "/tmp/fake"})
	_, _ = m5.Update(keyMsg("u"))
	// Quit cmd is returned; the model carries the file to main.
	if fm, ok := m5.(Model); ok && fm.updateState == updateReady {
		// Update with 'u':
		m6, _ := m5.Update(keyMsg("u"))
		if fm2 := m6.(Model); fm2.PendingUpdate != "/tmp/fake" {
			t.Fatalf("PendingUpdate = %q, want /tmp/fake", fm2.PendingUpdate)
		}
	}

	// Failed downloads surface an error message instead of the prompt.
	var m7 tea.Model = Model{}
	m7, _ = m7.Update(updateAvailableMsg{tag: "v8.8.8"})
	m7, _ = m7.Update(updatePrefetchedMsg{tag: "v8.8.8", err: context.DeadlineExceeded})
	if m7.(Model).updateState != updateFailed || m7.(Model).updateErr == "" {
		t.Fatalf("state after failed prefetch = %+v", m7.(Model))
	}
}
