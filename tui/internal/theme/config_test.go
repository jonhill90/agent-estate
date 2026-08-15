package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaultNoNotice(t *testing.T) {
	th, notice := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if th.ID != Default.ID {
		t.Fatalf("theme = %q, want %q", th.ID, Default.ID)
	}
	if notice != "" {
		t.Fatalf("notice = %q, want empty (a missing config is not an error)", notice)
	}
}

func TestLoadMalformedJSONReturnsDefaultWithNotice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	writeFile(t, path, `{not valid json`)

	th, notice := Load(path)
	if th.ID != Default.ID {
		t.Fatalf("theme = %q, want %q", th.ID, Default.ID)
	}
	if notice == "" {
		t.Fatal("notice is empty; a malformed config must say so visibly (#27 acceptance item 3)")
	}
}

func TestLoadUnknownThemeReturnsDefaultWithNotice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	writeFile(t, path, `{"theme": "does-not-exist"}`)

	th, notice := Load(path)
	if th.ID != Default.ID {
		t.Fatalf("theme = %q, want %q", th.ID, Default.ID)
	}
	if notice == "" {
		t.Fatal("notice is empty; an unknown theme name must say so visibly (#27 acceptance item 3)")
	}
}

func TestLoadKnownThemeNoNotice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	writeFile(t, path, `{"theme": "mono-contrast"}`)

	th, notice := Load(path)
	if th.ID != Mono.ID {
		t.Fatalf("theme = %q, want %q", th.ID, Mono.ID)
	}
	if notice != "" {
		t.Fatalf("notice = %q, want empty for a valid, known preference", notice)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "theme.json")
	if err := Save(path, Mono.ID); err != nil {
		t.Fatalf("Save: %v", err)
	}
	th, notice := Load(path)
	if th.ID != Mono.ID {
		t.Fatalf("theme = %q, want %q", th.ID, Mono.ID)
	}
	if notice != "" {
		t.Fatalf("notice = %q, want empty", notice)
	}
}

func TestSaveUnknownThemeErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	if err := Save(path, "not-a-real-theme"); err == nil {
		t.Fatal("Save with an unknown theme ID must error, never write a file Load can't resolve")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
}
