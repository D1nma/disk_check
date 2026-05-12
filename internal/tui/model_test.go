package tui

import (
	"testing"
	"github.com/D1nma/disk_check/internal/scanner"
)

func TestModelSortEntriesBySize(t *testing.T) {
	m := Model{
		Entries: []scanner.Entry{
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
		Entries: []scanner.Entry{
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
		Entries: []scanner.Entry{
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
