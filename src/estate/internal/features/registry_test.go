package features

import (
	"strings"
	"testing"
)

// TestRegistry_EveryDeliveredRowHasEvidence is the ledger's integrity gate:
// a Feature claiming Status == Delivered without a non-empty Evidence field
// naming a PR is exactly the drift this package exists to prevent -- a
// false "delivered" the operator would act on with nothing to check it
// against. This is the worse of the two failure directions (see
// TestRegistry_FlippingToDeliveredWithoutEvidenceFails below), so this test
// is written to fail loudly and by ID, not just by count.
func TestRegistry_EveryDeliveredRowHasEvidence(t *testing.T) {
	for _, f := range Registry {
		if f.Status != Delivered {
			continue
		}
		if strings.TrimSpace(f.Evidence) == "" {
			t.Errorf("feature %q (%s) is Delivered but carries no Evidence", f.ID, f.Name)
		}
	}
}

// TestRegistry_EveryStatusIsAKnownEnumValue guards against a typo'd status
// string silently sorting as neither delivered, in-progress nor
// not-started -- Status is a string type, so nothing at compile time stops
// e.g. "Delivered" (wrong case) from being assigned.
func TestRegistry_EveryStatusIsAKnownEnumValue(t *testing.T) {
	valid := map[Status]bool{Delivered: true, InProgress: true, NotStarted: true}
	for _, f := range Registry {
		if !valid[f.Status] {
			t.Errorf("feature %q has unrecognised status %q", f.ID, f.Status)
		}
	}
}

// TestRegistry_IDsAreUnique guards against a copy-pasted row silently
// shadowing another in any future lookup-by-ID code.
func TestRegistry_IDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Registry {
		if f.ID == "" {
			t.Errorf("feature %q has an empty ID", f.Name)
			continue
		}
		if seen[f.ID] {
			t.Errorf("duplicate feature ID %q", f.ID)
		}
		seen[f.ID] = true
	}
}

// TestRegistry_NonEmpty guards against an accidentally emptied slice
// silently reporting "no features" as if that were an honest state.
func TestRegistry_NonEmpty(t *testing.T) {
	if len(Registry) == 0 {
		t.Fatal("Registry must not be empty")
	}
}
