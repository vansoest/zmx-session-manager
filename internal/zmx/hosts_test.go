package zmx

import (
	"os"
	"path/filepath"
	"testing"
)

func writeHosts(t *testing.T, content string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "za")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hosts"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadHostsParsesAliases(t *testing.T) {
	writeHosts(t, "# comment\n\ndarkwing\nnas:196\nx270:212\n")
	got := LoadHosts()
	want := []string{"darkwing", "nas", "x270"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestLoadHostColorsParsesColorSuffix(t *testing.T) {
	writeHosts(t, "# comment\ndarkwing\nnas:196\nx270:212\n")
	colors := LoadHostColors()
	if colors["nas"] != "196" || colors["x270"] != "212" {
		t.Fatalf("got %v", colors)
	}
	if c, ok := colors["darkwing"]; ok {
		t.Fatalf("no suffix means no color, got %q", c)
	}
}
