package catalogue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDualLocatorsPreserveIdentityAndMissingLocal(t *testing.T) {
	dir := t.TempDir()
	reg := &Register{}
	now := time.Now()
	in := RegisterInput{Locator: "https://example.invalid/repo", ExtractionKind: ExtractionRepoDocs, RemoteURL: "https://example.invalid/repo", LocalPath: filepath.Join(dir, "missing"), RoutingSurfaces: []string{"README.md", "AGENTS.md"}}
	e, created := reg.Register(dir, in, now)
	if !created || e.ID != identityFor(in.Locator) || e.LocalState != LocalMissing {
		t.Fatalf("%+v", e)
	}
	if err := SaveRegister(dir, reg); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadRegister(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.Mkdir(in.LocalPath, 0700)
	os.WriteFile(filepath.Join(in.LocalPath, "README.md"), []byte("# fixture"), 0600)
	e, ok := reg.Refresh(dir, e.ID, now)
	if !ok || e.LocalState != LocalPresent || e.RemoteURL != in.RemoteURL || e.LocalPath != in.LocalPath {
		t.Fatalf("%+v", e)
	}
	e, created = reg.Register(dir, in, now)
	if created || len(reg.Entries) != 1 || e.ID != identityFor(in.Locator) {
		t.Fatal("duplicate or changed identity")
	}
	view := GenerateSourceView(e, now)
	if !strings.Contains(view, in.RemoteURL) || !strings.Contains(view, "AGENTS.md") {
		t.Fatal(view)
	}
}
func TestLocatorFieldsAreNeverInferred(t *testing.T) {
	reg := &Register{}
	e, _ := reg.Register(t.TempDir(), RegisterInput{Locator: "/a/local/repo"}, time.Now())
	if e.RemoteURL != "" || e.LocalPath != "" || e.LocalState != LocalUnspecified {
		t.Fatalf("invented locator: %+v", e)
	}
}
