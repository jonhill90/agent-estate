package catalogue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadRegister_MissingIsEmptyNotError(t *testing.T) {
	dir := t.TempDir()
	reg, err := LoadRegister(dir)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if len(reg.Entries) != 0 {
		t.Fatalf("Entries = %v, want empty", reg.Entries)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	reg := &Register{Entries: []RegisterEntry{{ID: "src-fixture", Kind: "kind/doc", ExtractionKind: ExtractionRepoDocs, Locator: "x"}}}
	if err := SaveRegister(dir, reg); err != nil {
		t.Fatalf("SaveRegister: %v", err)
	}
	got, err := LoadRegister(dir)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].ID != "src-fixture" {
		t.Fatalf("Entries = %v, want one src-fixture entry", got.Entries)
	}
}

// TestRegister_Idempotent is deliverable 4's own acceptance test:
// registering the same Kind+Locator twice must never create a second
// entry.
func TestRegister_Idempotent(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(docPath, []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := &Register{}
	in := RegisterInput{Kind: "kind/doc", ExtractionKind: ExtractionRepoDocs, Locator: docPath, WhyIndexed: "fixture"}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	e1, created1 := reg.Register(dir, in, now)
	if !created1 {
		t.Fatalf("first Register: created = false, want true")
	}
	e2, created2 := reg.Register(dir, in, now.Add(time.Minute))
	if created2 {
		t.Fatalf("second Register: created = true, want false (idempotent)")
	}
	if e1.ID != e2.ID {
		t.Fatalf("ID changed across re-registration: %q -> %q", e1.ID, e2.ID)
	}
	if len(reg.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1 (zero duplicate identities)", len(reg.Entries))
	}
}

// TestRegister_DifferentLocatorsDifferentIdentity is the mutation-style
// counterpart to Idempotent: two genuinely different sources must never
// collide onto one identity, which is what would happen if the identity
// comparison were broken (e.g. keyed on Kind alone).
func TestRegister_DifferentLocatorsDifferentIdentity(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := &Register{}
	now := time.Now()
	ea, _ := reg.Register(dir, RegisterInput{Kind: "kind/doc", ExtractionKind: ExtractionRepoDocs, Locator: a}, now)
	eb, _ := reg.Register(dir, RegisterInput{Kind: "kind/doc", ExtractionKind: ExtractionRepoDocs, Locator: b}, now)
	if ea.ID == eb.ID {
		t.Fatalf("two different locators produced the same id %q", ea.ID)
	}
	if len(reg.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(reg.Entries))
	}
}

// TestRefresh_FlipsToNeedsReviewOnChangedContent is deliverable 5's own
// acceptance test.
func TestRefresh_FlipsToNeedsReviewOnChangedContent(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(docPath, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := &Register{}
	now := time.Now()
	e, _ := reg.Register(dir, RegisterInput{Kind: "kind/doc", ExtractionKind: ExtractionRepoDocs, Locator: docPath}, now)
	if e.Status != StatusActive {
		t.Fatalf("initial Status = %v, want StatusActive", e.Status)
	}

	refreshed, ok := reg.Refresh(dir, e.ID, now.Add(time.Minute))
	if !ok {
		t.Fatalf("Refresh: entry not found")
	}
	if refreshed.Status != StatusActive {
		t.Fatalf("unchanged content: Status = %v, want StatusActive", refreshed.Status)
	}

	if err := os.WriteFile(docPath, []byte("v2 -- content actually changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	drifted, ok := reg.Refresh(dir, e.ID, now.Add(2*time.Minute))
	if !ok {
		t.Fatalf("Refresh: entry not found")
	}
	if drifted.Status != StatusNeedsReview {
		t.Fatalf("changed content: Status = %v, want StatusNeedsReview", drifted.Status)
	}
	if drifted.ObservedRevision == e.ObservedRevision {
		t.Fatalf("ObservedRevision unchanged despite content change")
	}
}

// TestRefresh_NeverOverwritesOperatorDeclaredFields proves a drift flip
// never touches Authority/Scope/etc -- those stay exactly what the
// operator declared until they explicitly re-register.
func TestRefresh_NeverOverwritesOperatorDeclaredFields(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(docPath, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := &Register{}
	now := time.Now()
	e, _ := reg.Register(dir, RegisterInput{Kind: "kind/doc", ExtractionKind: ExtractionRepoDocs, Locator: docPath, Authority: "operator-declared"}, now)

	if err := os.WriteFile(docPath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	drifted, _ := reg.Refresh(dir, e.ID, now.Add(time.Minute))
	if drifted.Authority != "operator-declared" {
		t.Fatalf("Authority = %q, want unchanged %q", drifted.Authority, "operator-declared")
	}
}

func TestAcknowledge_ClearsNeedsReviewWithoutRevisionChange(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.md")
	os.WriteFile(docPath, []byte("v1"), 0o644)
	reg := &Register{}
	now := time.Now()
	e, _ := reg.Register(dir, RegisterInput{Kind: "kind/doc", ExtractionKind: ExtractionRepoDocs, Locator: docPath}, now)
	os.WriteFile(docPath, []byte("v2"), 0o644)
	reg.Refresh(dir, e.ID, now.Add(time.Minute))

	acked, ok := reg.Acknowledge(e.ID, now.Add(2*time.Minute))
	if !ok {
		t.Fatalf("Acknowledge: entry not found")
	}
	if acked.Status != StatusActive {
		t.Fatalf("Status after Acknowledge = %v, want StatusActive", acked.Status)
	}
}

func TestShowAndList(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.md")
	os.WriteFile(docPath, []byte("v1"), 0o644)
	reg := &Register{}
	e, _ := reg.Register(dir, RegisterInput{Kind: "kind/doc", ExtractionKind: ExtractionRepoDocs, Locator: docPath}, time.Now())

	if got, ok := reg.Show(e.ID); !ok || got.ID != e.ID {
		t.Fatalf("Show(%q) = %v, %v", e.ID, got, ok)
	}
	if _, ok := reg.Show("src-doesnotexist"); ok {
		t.Fatalf("Show of unknown id unexpectedly found something")
	}
	if len(reg.List()) != 1 {
		t.Fatalf("List() = %v, want 1 entry", reg.List())
	}
}
