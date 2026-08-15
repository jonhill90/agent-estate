package theme

import (
	"os"
	"path/filepath"
	"strings"
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

// --- agent-tui#34: per-role colour overrides -------------------------------

func TestLoadColorOverrideAppliesOnlyOverriddenRole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	writeFile(t, path, `{"theme": "mono-contrast", "colors": {"error": "#123456"}}`)

	th, notice := Load(path)
	if notice != "" {
		t.Fatalf("notice = %q, want empty for a well-formed override", notice)
	}
	if th.ID != Mono.ID {
		t.Fatalf("theme ID = %q, want %q -- the base theme itself must not change", th.ID, Mono.ID)
	}
	if got := th.Color(RoleError); got != "#123456" {
		t.Fatalf("RoleError = %q, want the override %q", got, "#123456")
	}
	// Precedence: every role NOT named in "colors" keeps the base theme's
	// (Mono's) own colour -- a partial override must not silently behave
	// like a full one.
	if got, want := th.Color(RoleWarn), Mono.Color(RoleWarn); got != want {
		t.Fatalf("RoleWarn = %q, want Mono's own %q -- an unmentioned role must not change", got, want)
	}
	// The shipped Mono value itself must be untouched by this Load call.
	if got := Mono.Color(RoleError); got != "#ff0044" {
		t.Fatalf("Mono.Colors[RoleError] mutated to %q by Load -- base theme map was not cloned", got)
	}
}

func TestLoadColorOverrideWithNoThemeLayersOnDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	writeFile(t, path, `{"colors": {"error": "#123456"}}`)

	th, notice := Load(path)
	if notice != "" {
		t.Fatalf("notice = %q, want empty", notice)
	}
	if th.ID != Default.ID {
		t.Fatalf("theme ID = %q, want %q", th.ID, Default.ID)
	}
	if got := th.Color(RoleError); got != "#123456" {
		t.Fatalf("RoleError = %q, want override %q", got, "#123456")
	}
	if got, want := th.Color(RoleWarn), Default.Color(RoleWarn); got != want {
		t.Fatalf("RoleWarn = %q, want Default's own %q", got, want)
	}
}

func TestLoadColorMissingRoleNameProducesNoticeAndIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	writeFile(t, path, `{"colors": {"": "#123456"}}`)

	th, notice := Load(path)
	if notice == "" {
		t.Fatal("notice is empty; a colour entry with no role name must say so visibly (agent-tui#34)")
	}
	if got, want := th.Color(RoleError), Default.Color(RoleError); got != want {
		t.Fatalf("RoleError = %q, want Default's own %q -- an entry with no role name must not apply anywhere", got, want)
	}
}

func TestLoadColorUnknownRoleProducesNoticeAndIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	writeFile(t, path, `{"colors": {"nonexistent_role": "#123456"}}`)

	th, notice := Load(path)
	if notice == "" {
		t.Fatal("notice is empty; an unknown role name must say so visibly (agent-tui#34)")
	}
	if !strings.Contains(notice, "nonexistent_role") {
		t.Fatalf("notice %q does not name the unknown role", notice)
	}
	if th.ID != Default.ID {
		t.Fatalf("theme = %q, want %q -- an unknown role must not change which theme resolves", th.ID, Default.ID)
	}
}

func TestLoadColorInvalidValueProducesNoticeAndIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	writeFile(t, path, `{"colors": {"error": "not-a-colour"}}`)

	th, notice := Load(path)
	if notice == "" {
		t.Fatal("notice is empty; a value that is not a colour must say so visibly (agent-tui#34)")
	}
	if got, want := th.Color(RoleError), Default.Color(RoleError); got != want {
		t.Fatalf("RoleError = %q, want Default's own %q -- an invalid value must not apply", got, want)
	}
}

func TestLoadColorInvalidEntriesDoNotBlockValidOnes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	writeFile(t, path, `{"colors": {"error": "#123456", "bogus_role": "#ffffff", "warn": "still-not-a-colour"}}`)

	th, notice := Load(path)
	if notice == "" {
		t.Fatal("notice is empty; two bad entries in this file must both say so")
	}
	if !strings.Contains(notice, "bogus_role") || !strings.Contains(notice, "warn") {
		t.Fatalf("notice %q does not mention both bad entries", notice)
	}
	if got := th.Color(RoleError); got != "#123456" {
		t.Fatalf("RoleError = %q, want the valid override %q to still apply despite the other bad entries", got, "#123456")
	}
	if got, want := th.Color(RoleWarn), Default.Color(RoleWarn); got != want {
		t.Fatalf("RoleWarn = %q, want Default's own %q -- its bad value must not apply", got, want)
	}
}

// TestLoadColorBadValueShapesAllBehaveIdentically is agent-tui#36's fix:
// #34's review found six ways a "colors" entry's VALUE can be wrong, but
// only four of them (a bad string) were exercised before this test --
// a JSON number or nested object made json.Unmarshal fail the whole file
// via the old `map[string]string` decode, discarding "theme" and every
// other valid colour along with it. All six shapes, plus a valid control,
// must now behave identically: the bad role falls back to the base
// theme's own colour, every OTHER role in the same file still applies,
// "theme" itself still resolves, and the notice names the bad role.
func TestLoadColorBadValueShapesAllBehaveIdentically(t *testing.T) {
	shapes := []struct {
		name string
		json string // the raw JSON value for role "error"
	}{
		{"unquoted colour name", `"blue"`},
		{"malformed hex (bad chars)", `"#GGG"`},
		{"malformed hex (wrong length)", `"#12345"`},
		{"empty string", `""`},
		{"JSON number", `42`},
		{"nested object", `{"nested":"obj"}`},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "theme.json")
			body := `{"theme": "mono-contrast", "colors": {"error": ` + shape.json + `, "warn": "#123456"}}`
			writeFile(t, path, body)

			th, notice := Load(path)

			if th.ID != Mono.ID {
				t.Fatalf("theme = %q, want %q -- \"theme\" must still resolve despite the bad colour entry", th.ID, Mono.ID)
			}
			if got, want := th.Color(RoleError), Mono.Color(RoleError); got != want {
				t.Fatalf("RoleError = %q, want Mono's own %q -- the bad role must fall back", got, want)
			}
			if got := th.Color(RoleWarn); got != "#123456" {
				t.Fatalf("RoleWarn = %q, want the valid override %q -- other roles in the same file must still apply", got, "#123456")
			}
			if notice == "" {
				t.Fatal("notice is empty; a bad colour value must say so visibly")
			}
			if !strings.Contains(notice, "error") {
				t.Fatalf("notice %q does not name the bad role %q", notice, "error")
			}
		})
	}

	// Control: the same file shape with a VALID "error" entry must apply
	// it and stay silent -- proves the table above is testing rejection,
	// not some unrelated side effect of the file's shape.
	t.Run("valid control", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "theme.json")
		writeFile(t, path, `{"theme": "mono-contrast", "colors": {"error": "#654321", "warn": "#123456"}}`)

		th, notice := Load(path)

		if notice != "" {
			t.Fatalf("notice = %q, want empty for an all-valid file", notice)
		}
		if got := th.Color(RoleError); got != "#654321" {
			t.Fatalf("RoleError = %q, want the override %q", got, "#654321")
		}
		if got := th.Color(RoleWarn); got != "#123456" {
			t.Fatalf("RoleWarn = %q, want the override %q", got, "#123456")
		}
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
}
