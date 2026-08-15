package theme

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestAllThemesCoverEveryRole(t *testing.T) {
	roles := []Role{
		RoleError, RoleWarn, RoleFlag, RoleDirector, RoleUnsupervised,
		RoleSelectedBG, RoleBorder, RoleBacklog, RoleInProgress, RoleInReview,
		RoleBlocked, RoleDone, RoleNeutral,
	}
	for _, th := range All {
		for _, r := range roles {
			if th.Color(r) == "" {
				t.Errorf("theme %q has no colour for role %q", th.ID, r)
			}
		}
	}
}

func TestMonoDiffersFromDefaultOnEveryRole(t *testing.T) {
	// #27: "one theme proves nothing" -- Mono exists to make a missed
	// literal visible, which only works if it disagrees with Default on
	// every single role, not just some of them.
	for role := range Default.Colors {
		if Default.Color(role) == Mono.Color(role) {
			t.Errorf("role %q: Default and Mono share colour %q -- a literal routed to this role couldn't fail its mutation check", role, Default.Color(role))
		}
	}
	if Default.Border.Top == Mono.Border.Top {
		t.Error("Default and Mono share a border style -- border-character routing couldn't be proven")
	}
	if Default.Padding == Mono.Padding {
		t.Error("Default and Mono share a padding value -- padding routing couldn't be proven")
	}
	if Default.DirectorMark == Mono.DirectorMark {
		t.Error("Default and Mono share a director mark -- glyph-rune routing couldn't be proven")
	}
}

func TestByID(t *testing.T) {
	if got, ok := ByID(Mono.ID); !ok || got.ID != Mono.ID {
		t.Fatalf("ByID(%q) = %v, %v; want Mono, true", Mono.ID, got, ok)
	}
	if got, ok := ByID("does-not-exist"); ok || got.ID != Default.ID {
		t.Fatalf("ByID(unknown) = %v, %v; want Default, false", got, ok)
	}
}

func TestColorMissingRoleReturnsEmpty(t *testing.T) {
	th := Theme{Colors: map[Role]lipgloss.Color{}}
	if got := th.Color(RoleError); got != "" {
		t.Fatalf("Color(missing role) = %q, want empty", got)
	}
}
