package knowledge

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/catalogue"
)

func TestCatalogueSource_EmptyPathIsHonestlyUnconfigured(t *testing.T) {
	res, items := catalogueSource("")
	if res.OK {
		t.Fatalf("OK = true, want false for an unconfigured catalogue path")
	}
	if res.Reason == "" {
		t.Fatalf("Reason is empty for a failed source")
	}
	if items != nil {
		t.Fatalf("items = %v, want nil", items)
	}
}

func TestCatalogueSource_EmptyRegisterIsOKWithZeroItems(t *testing.T) {
	res, items := catalogueSource(t.TempDir())
	if !res.OK {
		t.Fatalf("OK = false, want true for a register directory that simply has nothing registered yet")
	}
	if len(items) != 0 {
		t.Fatalf("items = %v, want none", items)
	}
}

func TestCatalogueSource_ReadsRegisteredEntries(t *testing.T) {
	dir := t.TempDir()
	reg := &catalogue.Register{}
	reg.Register(dir, catalogue.RegisterInput{
		Kind:           "kind/doc",
		ExtractionKind: catalogue.ExtractionRepoDocs,
		Locator:        filepath.Join(dir, "doc.md"),
		WhyIndexed:     "fixture reason",
		Authority:      "fixture authority",
	}, time.Now())
	if err := catalogue.SaveRegister(dir, reg); err != nil {
		t.Fatalf("SaveRegister: %v", err)
	}

	res, items := catalogueSource(dir)
	if !res.OK || res.Count != 1 {
		t.Fatalf("res = %+v, want OK=true Count=1", res)
	}
	if len(items) != 1 {
		t.Fatalf("items = %v, want 1", items)
	}
	it := items[0]
	if it.Source != "catalogue-source" {
		t.Fatalf("Source = %q, want catalogue-source", it.Source)
	}
	if it.Tier1 != "fixture reason" {
		t.Fatalf("Tier1 = %q, want the entry's own WhyIndexed", it.Tier1)
	}
	if it.Publishable {
		t.Fatalf("Publishable = true, want false -- catalogue-source defaults private")
	}
}

// TestCatalogueSource_ItemIDStableAcrossTwoReads mirrors id_test.go's own
// stability requirement for the other five sources.
func TestCatalogueSource_ItemIDStableAcrossTwoReads(t *testing.T) {
	dir := t.TempDir()
	reg := &catalogue.Register{}
	reg.Register(dir, catalogue.RegisterInput{Kind: "kind/doc", ExtractionKind: catalogue.ExtractionRepoDocs, Locator: filepath.Join(dir, "doc.md")}, time.Now())
	catalogue.SaveRegister(dir, reg)

	_, items1 := catalogueSource(dir)
	_, items2 := catalogueSource(dir)
	if items1[0].ID != items2[0].ID {
		t.Fatalf("ID changed across two reads: %q vs %q", items1[0].ID, items2[0].ID)
	}
}
