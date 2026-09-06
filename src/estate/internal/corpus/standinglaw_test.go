package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixtureFact writes a synthetic vault fact file (never the real one --
// vault content is private and test fixtures here are synthesised, never
// copied from an operator fact) and returns its sha256 hex digest, so a
// test can declare a StandingLawMember whose HashPrefix actually matches.
func writeFixtureFact(t *testing.T, vaultDir, slug, body string) string {
	t.Helper()
	dir := filepath.Join(vaultDir, "agent", "facts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ntype: fixture\ntitle: " + slug + "\n---\n" + body + "\n"
	path := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// withStandingLawSet swaps StandingLawSet for the duration of a test and
// restores it afterward, mirroring corpus.go's own maxMatches/
// maxPreambleBytes save-mutate-restore pattern.
func withStandingLawSet(t *testing.T, set []StandingLawMember) {
	t.Helper()
	orig := StandingLawSet
	StandingLawSet = set
	t.Cleanup(func() { StandingLawSet = orig })
}

// TestStandingLawResolvesOnlyDeclaredMembers proves membership is
// declarative: a vault directory can hold any number of fact files, but
// StandingLaw() -- and therefore Grounding()'s standing-law section --
// surfaces ONLY the slug(s) named in StandingLawSet, never a fact merely
// because it exists in the same vault (agent-estate#1255 requirement 5,
// and the non-member-never-reaches-the-preamble requirement 3).
func TestStandingLawResolvesOnlyDeclaredMembers(t *testing.T) {
	vault := t.TempDir()
	memberHash := writeFixtureFact(t, vault, "member-fact", "member body text -- this one is declared law.")
	writeFixtureFact(t, vault, "bystander-fact", "bystander body text -- present in the vault, never declared.")

	withStandingLawSet(t, []StandingLawMember{
		{Slug: "member-fact", HashPrefix: memberHash[:12], Reason: "fixture reason"},
	})

	entries, err := StandingLaw(vault)
	if err != nil {
		t.Fatalf("StandingLaw() returned an error against a valid fixture: %v", err)
	}
	if len(entries) != 1 || entries[0].Slug != "member-fact" {
		t.Fatalf("StandingLaw() resolved %+v, want exactly the one declared member", entries)
	}
	for _, e := range entries {
		if strings.Contains(e.Body, "bystander") {
			t.Fatalf("StandingLaw() surfaced the non-member fact's body: %+v", e)
		}
	}

	g := Grounding("some task", nil, nil, entries)
	if strings.Contains(g, "bystander") {
		t.Fatalf("Grounding() rendered a non-member vault fact into the preamble:\n%s", g)
	}
	if !strings.Contains(g, "member body text") {
		t.Fatalf("Grounding() dropped the declared member's body:\n%s", g)
	}
}

// TestStandingLawFactPresentButNotDeclaredNeverBecomesLaw is
// agent-estate#1255 requirement 5 stated the other direction: publishing a
// fact to the vault is NOT, by itself, an act of declaring it standing
// law. Only StandingLawSet membership is.
func TestStandingLawFactPresentButNotDeclaredNeverBecomesLaw(t *testing.T) {
	vault := t.TempDir()
	writeFixtureFact(t, vault, "undeclared-fact", "this fact exists in the vault and nowhere else.")

	withStandingLawSet(t, nil) // nothing declared

	entries, err := StandingLaw(vault)
	if err != nil {
		t.Fatalf("StandingLaw() with an empty declared set returned an error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("StandingLaw() resolved %+v from an undeclared vault fact -- publication alone must never become law", entries)
	}
}

// TestStandingLawMemberCapIsEnforced is agent-estate#1255 requirement 4:
// exceeding MaxStandingLawMembers is a hard failure, not a warning and not
// a silent truncation to the first N members.
func TestStandingLawMemberCapIsEnforced(t *testing.T) {
	vault := t.TempDir()
	h1 := writeFixtureFact(t, vault, "fact-one", "body one")
	h2 := writeFixtureFact(t, vault, "fact-two", "body two")
	withStandingLawSet(t, []StandingLawMember{
		{Slug: "fact-one", HashPrefix: h1[:12], Reason: "fixture reason one"},
		{Slug: "fact-two", HashPrefix: h2[:12], Reason: "fixture reason two"},
	})

	origCap := MaxStandingLawMembers
	MaxStandingLawMembers = 1
	defer func() { MaxStandingLawMembers = origCap }()

	if _, err := StandingLaw(vault); err == nil {
		t.Fatal("StandingLaw() accepted a 2-member set against a cap of 1 -- the member cap must refuse, not truncate")
	}
}

// TestStandingLawByteCapIsEnforced mirrors the member-cap test for total
// rendered bytes: a single oversized member (or several small ones
// together) must refuse once the declared set's total body size exceeds
// MaxStandingLawBytes.
func TestStandingLawByteCapIsEnforced(t *testing.T) {
	vault := t.TempDir()
	big := strings.Repeat("x", 5000)
	h := writeFixtureFact(t, vault, "oversized-fact", big)
	withStandingLawSet(t, []StandingLawMember{
		{Slug: "oversized-fact", HashPrefix: h[:12], Reason: "fixture reason"},
	})

	origCap := MaxStandingLawBytes
	MaxStandingLawBytes = 100
	defer func() { MaxStandingLawBytes = origCap }()

	if _, err := StandingLaw(vault); err == nil {
		t.Fatal("StandingLaw() accepted a member whose body exceeds the byte cap -- the byte cap must refuse, not truncate")
	}
}

// TestStandingLawCapsAreLoadBearing is the member/byte caps' own §4(c)-style
// proof, mirroring TestGroundingCapIsLoadBearing: run once with the real
// (generous) caps and once with them lowered below the fixture's actual
// size, and confirm the lowered run is the one that fails -- so the cap
// enforcement above is proven to be doing real work, not vacuously true
// because the fixture never approached any cap.
func TestStandingLawCapsAreLoadBearing(t *testing.T) {
	vault := t.TempDir()
	h := writeFixtureFact(t, vault, "normal-fact", "an ordinary, small fixture body.")
	withStandingLawSet(t, []StandingLawMember{
		{Slug: "normal-fact", HashPrefix: h[:12], Reason: "fixture reason"},
	})

	if _, err := StandingLaw(vault); err != nil {
		t.Fatalf("StandingLaw() failed under the real caps against a tiny fixture: %v", err)
	}

	origMembers, origBytes := MaxStandingLawMembers, MaxStandingLawBytes
	MaxStandingLawMembers = 0
	defer func() { MaxStandingLawMembers, MaxStandingLawBytes = origMembers, origBytes }()

	if _, err := StandingLaw(vault); err == nil {
		t.Fatal("StandingLaw() succeeded with MaxStandingLawMembers=0 against a 1-member set -- the cap is not load-bearing")
	}
}

// TestStandingLawHashDriftRefuses proves the hash-integrity guard: a vault
// fact whose bytes no longer match the hash recorded at declaration time
// must refuse rather than silently inject the drifted body as if it were
// still the reviewed version.
func TestStandingLawHashDriftRefuses(t *testing.T) {
	vault := t.TempDir()
	writeFixtureFact(t, vault, "drifted-fact", "original reviewed body")
	withStandingLawSet(t, []StandingLawMember{
		{Slug: "drifted-fact", HashPrefix: "deadbeefdead", Reason: "fixture reason"},
	})

	if _, err := StandingLaw(vault); err == nil {
		t.Fatal("StandingLaw() accepted a member whose file hash does not match the declared prefix")
	}
}

// TestStandingLawMissingVaultWithDeclaredMembersRefuses proves declared law
// that cannot be resolved stops resolution rather than silently proceeding
// one binding constraint short, mirroring Hard()'s own unreadable-corpus
// refusal.
func TestStandingLawMissingVaultWithDeclaredMembersRefuses(t *testing.T) {
	withStandingLawSet(t, []StandingLawMember{
		{Slug: "anything", HashPrefix: "abc", Reason: "fixture reason"},
	})
	if _, err := StandingLaw(""); err == nil {
		t.Fatal("StandingLaw() with declared members and no vault configured must refuse, not silently proceed")
	}
}

// TestStandingLawEmptySetWithNoVaultIsFine proves the converse: no vault
// and nothing declared is a legitimate, error-free empty result -- there is
// no law to resolve, which is different from law existing but being
// unreadable.
func TestStandingLawEmptySetWithNoVaultIsFine(t *testing.T) {
	withStandingLawSet(t, nil)
	entries, err := StandingLaw("")
	if err != nil {
		t.Fatalf("StandingLaw() with nothing declared and no vault returned an error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("StandingLaw() with nothing declared returned entries: %+v", entries)
	}
}

// TestGroundingLabelsStandingLawSeparatelyFromCorpusLaw proves requirement
// 3: the rendered preamble marks standing law as Agent-Memory-sourced,
// distinct from the "OPERATOR PARAMETERS" corpus section, so an agent can
// tell which is which.
func TestGroundingLabelsStandingLawSeparatelyFromCorpusLaw(t *testing.T) {
	ps := []Param{{Key: "tooling=cli_first", Body: "Prefer CLI-backed workflows."}}
	standing := []StandingLawEntry{
		{Slug: "member-fact", Reason: "fixture reason", Body: "member body text"},
	}
	g := Grounding("some task", ps, nil, standing)
	if !strings.Contains(g, "OPERATOR PARAMETERS -- THESE ARE LAW") {
		t.Fatalf("corpus law heading missing:\n%s", g)
	}
	if !strings.Contains(g, "Standing law -- Agent Memory") {
		t.Fatalf("standing-law section is not separately labelled:\n%s", g)
	}
	if !strings.Contains(g, "member body text") || !strings.Contains(g, "fixture reason") {
		t.Fatalf("standing-law entry body/reason missing from preamble:\n%s", g)
	}
}

// TestGroundingOmitsStandingLawSectionWhenNoneDeclared proves the section
// does not appear at all (rather than an empty, misleading header) when
// StandingLaw() resolved nothing.
func TestGroundingOmitsStandingLawSectionWhenNoneDeclared(t *testing.T) {
	ps := []Param{{Key: "k", Body: "b"}}
	g := Grounding("some task", ps, nil, nil)
	if strings.Contains(g, "Standing law -- Agent Memory") {
		t.Fatalf("standing-law section rendered with nothing declared:\n%s", g)
	}
}

// TestGroundingIncludesAttributionInstructionWhenStandingLawPresent is
// agent-estate#1255's re-measurement requirement: attribution must be part
// of the standing-law contract, not a hoped-for behaviour, so the rendered
// preamble must actually carry the instruction whenever a member is
// injected, and must not carry it when the set is empty.
func TestGroundingIncludesAttributionInstructionWhenStandingLawPresent(t *testing.T) {
	standing := []StandingLawEntry{
		{Slug: "member-fact", Reason: "fixture reason", Body: "member body text"},
	}
	g := Grounding("some task", nil, nil, standing)
	if !strings.Contains(g, StandingLawAttributionInstruction) {
		t.Fatalf("Grounding() with a standing-law member did not render the attribution instruction:\n%s", g)
	}

	empty := Grounding("some task", nil, nil, nil)
	if strings.Contains(empty, StandingLawAttributionInstruction) {
		t.Fatalf("Grounding() with no standing-law members rendered the attribution instruction anyway:\n%s", empty)
	}
}

// TestStandingLawByteCapCountsAttributionInstruction proves the fixed
// attribution instruction counts against MaxStandingLawBytes like a
// member's own body -- it is not free just because it is not a vault fact.
func TestStandingLawByteCapCountsAttributionInstruction(t *testing.T) {
	vault := t.TempDir()
	// Body sized to fit alone under the cap, but not alongside the fixed
	// attribution instruction.
	body := strings.Repeat("x", MaxStandingLawBytes-len(StandingLawAttributionInstruction)+1)
	h := writeFixtureFact(t, vault, "snug-fact", body)
	withStandingLawSet(t, []StandingLawMember{
		{Slug: "snug-fact", HashPrefix: h[:12], Reason: "fixture reason"},
	})

	if _, err := StandingLaw(vault); err == nil {
		t.Fatal("StandingLaw() accepted a member whose body fits alone but overflows once the attribution instruction is counted")
	}
}
