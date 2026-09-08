package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// liveVaultOrSkip is the shared precondition for every test in this file:
// they all read the REAL vault at $AGENT_MEMORY_VAULT, never a fixture,
// because their whole purpose (agent-estate#1286) is catching a bulk vault
// write that mutated the real, declared standing-law member's real bytes
// -- a synthetic fixture cannot stand in for that any more than a
// synthetic vault could have told us today's tagging pass came within 7
// files of an estate-wide outage.
//
// When $AGENT_MEMORY_VAULT is unset, this skips LOUDLY rather than
// passing silently. That distinction matters: agent-estate#1321 was filed
// about a test whose skip looked, from the outside, indistinguishable
// from a pass, so a green CI run implied a guard that had actually
// checked nothing; #1323's own fix was criticised for asserting LESS in
// CI than locally without saying so plainly enough. This is a different
// shape from both, not the same mistake repeated: #1321/#1323 skipped a
// measurement that was merely unavailable on one OS (no vm_stat/sysctl)
// but whose SUBJECT -- host memory pressure -- still existed on that
// machine, reachable by some other measurement a smarter test could have
// used instead. Here the subject itself -- the operator's real,
// iCloud-synced Obsidian vault -- is not merely hard to reach from a
// GitHub Actions runner, it is structurally, deliberately absent: it is
// private content that is never checked into this repo (see
// standinglaw_test.go's own writeFixtureFact comment: "vault content is
// private and test fixtures here are synthesised, never copied from an
// operator fact") and must never exist on a public CI runner. There is
// no substitute a CI test could read instead without either fabricating
// content that cannot actually prove the real file didn't drift, or
// leaking private vault content into a public CI log -- both worse than
// an honest, visible skip. See TestLiveStandingLawMemberHashHasNotDrifted's
// own doc comment for where this test's real coverage actually comes
// from instead.
func liveVaultOrSkip(t *testing.T) string {
	t.Helper()
	vault := os.Getenv("AGENT_MEMORY_VAULT")
	if vault == "" {
		t.Skip("AGENT_MEMORY_VAULT is not set -- SKIPPING, not passing: " +
			"this run checks nothing. The real vault is private, uncommitted " +
			"content that cannot and must not exist on this machine (see this " +
			"test file's own package doc comment for why that is not the " +
			"agent-estate#1321/#1323 shape). This test's actual coverage comes " +
			"from running locally, where $AGENT_MEMORY_VAULT is set in every " +
			"dispatched turn's environment on this estate, and `go test " +
			"./src/estate/...` is a mandatory step of every task's own Verify " +
			"section -- so the very next dispatch to run its own tests, not " +
			"only the one that caused the drift, fails here with a named, " +
			"attributable message instead of the estate's next unrelated " +
			"dispatch dying at os.Exit(1) days later.")
	}
	return vault
}

// TestLiveStandingLawMemberHashHasNotDrifted is agent-estate#1286's own
// named deliverable: read the live declared member file and confirm its
// hash still starts with HashPrefix. This calls StandingLaw() itself --
// the exact function main.go calls before every dispatch -- rather than
// re-implementing the hash comparison a second time: a second,
// independent copy of that check could itself silently drift from what
// production actually enforces, which is exactly the kind of unnoticed
// gap agent-estate#1286 is about. A failure here names the member and the
// drifted hash directly, in a `go test` any task's Verify step already
// runs, instead of an opaque os.Exit(1) inside a completely unrelated
// dispatch.
func TestLiveStandingLawMemberHashHasNotDrifted(t *testing.T) {
	vault := liveVaultOrSkip(t)
	if _, err := StandingLaw(vault); err != nil {
		t.Fatalf("StandingLaw() failed against the real, live vault -- a "+
			"declared standing-law member has drifted (or otherwise stopped "+
			"resolving), and every dispatch on this estate will refuse to "+
			"start with os.Exit(1) until it is re-reviewed and re-pinned "+
			"(agent-estate#1286): %v", err)
	}
}

// TestLiveStandingLawMemberHashCheckCatchesRealMutation is this task's own
// required "prove it bites": read-only copies the REAL declared member's
// current bytes into a throwaway t.TempDir() fixture (never writing back
// to the real file -- agent-estate#1286's own hard constraint), flips one
// byte, and confirms StandingLaw() refuses the mutated copy specifically
// on hash drift. TestStandingLawHashDriftRefuses elsewhere in this
// package already proves the mechanism works against a simplified
// synthetic fixture; this proves it against the member's own real,
// current shape -- frontmatter, aliases line, and all -- so the
// demonstration is of the actual guard biting on the actual content it
// protects, not a stand-in for it.
func TestLiveStandingLawMemberHashCheckCatchesRealMutation(t *testing.T) {
	vault := liveVaultOrSkip(t)
	for _, m := range StandingLawSet {
		_, raw, err := resolveStandingLawMemberFile(vault, m.Slug)
		if err != nil {
			t.Fatalf("could not read the real member %q to copy for mutation: %v", m.Slug, err)
		}

		mutated := append([]byte(nil), raw...)
		if len(mutated) == 0 {
			t.Fatalf("real member %q resolved to zero bytes -- cannot mutate an empty file", m.Slug)
		}
		mutated[len(mutated)-1] ^= 0xFF // flip the body's last byte; frontmatter sits at the start and is untouched

		fixtureVault := t.TempDir()
		dir := filepath.Join(fixtureVault, "01 - Notes")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "mutated-copy.md"), mutated, 0o600); err != nil {
			t.Fatal(err)
		}

		withStandingLawSet(t, []StandingLawMember{
			{Slug: m.Slug, HashPrefix: m.HashPrefix, Reason: m.Reason},
		})

		_, err = StandingLaw(fixtureVault)
		if err == nil {
			t.Fatalf("StandingLaw() accepted a one-byte mutation of the real member %q -- the hash-drift guard is not load-bearing", m.Slug)
		}
		if !strings.Contains(err.Error(), "drifted") {
			t.Fatalf("StandingLaw() rejected the mutated copy of %q for a reason other than hash drift, so this did not actually exercise the guard: %v", m.Slug, err)
		}
	}
}
