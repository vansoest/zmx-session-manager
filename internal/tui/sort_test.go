package tui

import (
	"testing"
)

func sortFixture() ([]Session, *Model) {
	m := NewModel()
	m.sessions = []Session{
		{Name: "zulu", Host: "alpha", Uptime: 10},
		{Name: "mike", Host: "alpha", Uptime: 30},
		{Name: "local", Uptime: 20},
		{Name: "bravo", Host: "zeta", Uptime: 40},
	}
	m.markSessionsChanged()
	return m.sessions, &m
}

func TestSortByHostGroupsRemoteHosts(t *testing.T) {
	_, m := sortFixture()
	m.sortMode = sortByHost
	got := m.visibleSessions()
	want := []struct {
		host, name string
	}{
		{"", "local"},
		{"alpha", "mike"},
		{"alpha", "zulu"},
		{"zeta", "bravo"},
	}
	for i, w := range want {
		if got[i].Host != w.host || got[i].Name != w.name {
			t.Fatalf("row %d = {%q %q}, want {%q %q}", i, got[i].Host, got[i].Name, w.host, w.name)
		}
	}
	if m.sortMode.label() != "host" {
		t.Fatalf("label = %q, want %q", m.sortMode.label(), "host")
	}
}

func TestDefaultSortIsUptime(t *testing.T) {
	if NewModel().sortMode != sortByUptime {
		t.Fatalf("default sort mode = %v, want sortByUptime", NewModel().sortMode)
	}
}
