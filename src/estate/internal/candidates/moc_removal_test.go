package candidates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mocDraftFixture writes a single Inbox MOC draft directly (bypassing
// MOCProposals' own 8-note threshold, irrelevant to what this file tests)
// and returns its basename.
func mocDraftFixture(t *testing.T, v, tag string) string {
	t.Helper()
	if e := os.MkdirAll(filepath.Join(v, "00 - Inbox"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(v, "99 - Meta"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(v, "99 - Meta/tags.md")); os.IsNotExist(e) {
		os.WriteFile(filepath.Join(v, "99 - Meta/tags.md"), []byte("`"+tag+"`"), 0600)
	}
	name := "moc-" + tag + ".md"
	body := "---\ntype: MOC\nid: " + scalar(strings.TrimSuffix(name, ".md")) + "\ntitle: " + scalar(tag) +
		"\ndescription: \"d\"\ntags: [" + scalar(tag) + "]\ncreated: 2026-09-08T00:00:00Z\nupdated: 2026-09-08T00:00:00Z\nstatus: draft\nsource: \"Derived from cited stable notes\"\n---\n\n# " +
		tag + "\n\n## Overview\n\nReview these connections before accepting.\n\n<!-- generated-links:start -->\n<!-- generated-links:end -->\n"
	if e := os.WriteFile(filepath.Join(v, "00 - Inbox", name), []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	return name
}

// TestReviewMOCRecordsPromotedTo pins the reciprocal pointer ReviewMOC's
// accept branch now writes (agent-estate#1284) -- without it,
// RemoveMOCDraft has no way to confirm an accepted draft's durable copy
// actually landed before removing the draft.
func TestReviewMOCRecordsPromotedTo(t *testing.T) {
	v := t.TempDir()
	name := mocDraftFixture(t, v, "gate")
	if e := ReviewMOC(v, name, "process:test", true, true); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(v, "00 - Inbox", name))
	if e != nil {
		t.Fatal(e)
	}
	if got := field(string(raw), "promoted_to"); got != "02 - MOCs/gate.md" {
		t.Fatalf("promoted_to = %q, want \"02 - MOCs/gate.md\"", got)
	}
}

// TestRemoveMOCDraftClosesTheUnreachableStub is agent-estate#1284
// finding 1's own reproduction: ReviewMOC accepts a draft, which the
// issue measured leaves 42 such stubs stuck in 00 - Inbox because
// ReviewMOC's own guard (status != "draft") refuses to touch a draft it
// already reviewed. Confirms that failure mode still holds (ReviewMOC is
// deliberately NOT changed to reach into its own past output), then
// confirms RemoveMOCDraft is the verb that actually clears it -- file
// gone, backed up, logged.
func TestRemoveMOCDraftClosesTheUnreachableStub(t *testing.T) {
	v := t.TempDir()
	name := mocDraftFixture(t, v, "gate")
	draftPath := filepath.Join(v, "00 - Inbox", name)
	if e := ReviewMOC(v, name, "process:test", true, true); e != nil {
		t.Fatal(e)
	}
	// The exact defect: ReviewMOC cannot reach its own deprecated output.
	if e := ReviewMOC(v, name, "process:test", true, true); e == nil {
		t.Fatal("ReviewMOC re-accepted an already-deprecated draft; expected \"not a draft MOC\"")
	} else if !strings.Contains(e.Error(), "not a draft MOC") {
		t.Fatalf("wrong refusal reason: %v", e)
	}
	before, e := os.ReadFile(draftPath)
	if e != nil {
		t.Fatal(e)
	}

	if e := RemoveMOCDraft(v, name, "accepted and superseded by the live hub; regenerable stub", true); e != nil {
		t.Fatalf("RemoveMOCDraft on a promoted, stable draft: %v", e)
	}
	if _, e := os.Stat(draftPath); !os.IsNotExist(e) {
		t.Fatalf("draft still present after removal: err=%v", e)
	}

	// Backed up before removal -- the discipline the issue asks to live
	// inside the verb, not the operator's memory.
	backups, e := filepath.Glob(filepath.Join(v, "99 - Meta/.inmaps-backup-*/*"))
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, b := range backups {
		c, _ := os.ReadFile(b)
		if string(c) == string(before) {
			found = true
		}
	}
	if !found {
		t.Fatal("removed draft's exact bytes are not in any backup directory")
	}

	log, e := os.ReadFile(filepath.Join(v, "99 - Meta/log.md"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(log), "**Delete**") || !strings.Contains(string(log), "00 - Inbox/"+name) {
		t.Fatalf("log.md does not record the deletion: %s", log)
	}
	if !strings.Contains(string(log), "regenerable stub") {
		t.Fatalf("log.md does not carry the stated reason: %s", log)
	}
}

// TestRemoveMOCDraftRefusesWhenPromotedHubMissing is the safety condition
// agent-estate#1284 asks for explicitly: refuse anything carrying
// independent provenance not yet durably landed. A draft that CLAIMS
// promotion but whose hub is gone or unreadable must not be removed --
// that would delete the only remaining copy.
func TestRemoveMOCDraftRefusesWhenPromotedHubMissing(t *testing.T) {
	v := t.TempDir()
	name := mocDraftFixture(t, v, "gate")
	if e := ReviewMOC(v, name, "process:test", true, true); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(v, "02 - MOCs/gate.md")); e != nil {
		t.Fatal(e)
	}
	if e := RemoveMOCDraft(v, name, "hub is gone, should still refuse", true); e == nil {
		t.Fatal("removed a draft whose promoted hub does not exist")
	}
	if _, e := os.Stat(filepath.Join(v, "00 - Inbox", name)); e != nil {
		t.Fatalf("draft was removed despite the refusal: %v", e)
	}
}

// TestRemoveMOCDraftRefusesWhenPromotedHubNotStable is the same safety
// condition against a hub that exists but was not (or no longer)
// confirmed live.
func TestRemoveMOCDraftRefusesWhenPromotedHubNotStable(t *testing.T) {
	v := t.TempDir()
	name := mocDraftFixture(t, v, "gate")
	if e := ReviewMOC(v, name, "process:test", true, true); e != nil {
		t.Fatal(e)
	}
	hub := filepath.Join(v, "02 - MOCs/gate.md")
	raw, e := os.ReadFile(hub)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(hub, []byte(replaceField(string(raw), "status", "draft")), 0600)
	if e := RemoveMOCDraft(v, name, "hub not stable, should still refuse", true); e == nil {
		t.Fatal("removed a draft whose promoted hub is not status: stable")
	}
}

// TestRemoveMOCDraftRemovesARejectedDraftToo confirms the OTHER branch --
// a rejected draft carries no promoted_to at all, and the issue's own
// argument is that its content is regenerable navigation furniture with
// no provenance worth keeping, so it is removable too, once reviewed and
// given a reason.
func TestRemoveMOCDraftRemovesARejectedDraftToo(t *testing.T) {
	v := t.TempDir()
	name := mocDraftFixture(t, v, "gate")
	if e := ReviewMOC(v, name, "process:test", false, true); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(filepath.Join(v, "00 - Inbox", name))
	if field(string(raw), "promoted_to") != "" {
		t.Fatalf("a rejected draft carries promoted_to: %s", raw)
	}
	if e := RemoveMOCDraft(v, name, "rejected, topic not worth a hub", true); e != nil {
		t.Fatalf("RemoveMOCDraft on a rejected draft: %v", e)
	}
	if _, e := os.Stat(filepath.Join(v, "00 - Inbox", name)); !os.IsNotExist(e) {
		t.Fatalf("rejected draft still present: err=%v", e)
	}
}

// TestRemoveMOCDraftRefusesADraftStillUnderReview is RemoveMOCDraft's own
// scope boundary: a draft ReviewMOC has not touched yet is ReviewMOC's
// job, not this one -- removing it would destroy a proposal nobody
// decided on.
func TestRemoveMOCDraftRefusesADraftStillUnderReview(t *testing.T) {
	v := t.TempDir()
	name := mocDraftFixture(t, v, "gate")
	if e := RemoveMOCDraft(v, name, "should still refuse", true); e == nil {
		t.Fatal("removed a draft that was never reviewed")
	}
	if _, e := os.Stat(filepath.Join(v, "00 - Inbox", name)); e != nil {
		t.Fatalf("undeciced draft was removed: %v", e)
	}
}

// TestRemoveMOCDraftRequiresAReason is agent-estate#1284's own rule
// applied to the tool, not just a hand edit: it-9552ed57c7f60ca -- a
// removal must say why, not just that.
func TestRemoveMOCDraftRequiresAReason(t *testing.T) {
	v := t.TempDir()
	name := mocDraftFixture(t, v, "gate")
	if e := ReviewMOC(v, name, "process:test", true, true); e != nil {
		t.Fatal(e)
	}
	if e := RemoveMOCDraft(v, name, "", true); e == nil {
		t.Fatal("removed a draft with no stated reason")
	}
	if e := RemoveMOCDraft(v, name, "   ", true); e == nil {
		t.Fatal("removed a draft with a whitespace-only reason")
	}
}

// TestRemoveMOCDraftRefusesOutsideMOCNamespace confirms the same
// basename/prefix/suffix scoping ReviewMOC itself uses -- this verb must
// never be usable to remove an arbitrary vault path.
func TestRemoveMOCDraftRefusesOutsideMOCNamespace(t *testing.T) {
	v := t.TempDir()
	os.MkdirAll(filepath.Join(v, "01 - Notes/01f - Facts"), 0700)
	target := filepath.Join(v, "01 - Notes/01f - Facts/20260908000000.md")
	os.WriteFile(target, []byte("---\ntype: Fact\nstatus: deprecated\n---\n"), 0600)
	for _, bad := range []string{
		"../01 - Notes/01f - Facts/20260908000000.md",
		"moc-x.md/../../01 - Notes/01f - Facts/20260908000000.md",
		"not-moc-prefixed.md",
		"moc-no-suffix",
	} {
		if e := RemoveMOCDraft(v, bad, "should refuse", true); e == nil {
			t.Fatalf("accepted an out-of-namespace name: %q", bad)
		}
	}
	if _, e := os.Stat(target); e != nil {
		t.Fatalf("an unrelated fact was touched: %v", e)
	}
}

// TestRemoveMOCDraftDryRunWritesNothing matches every other verb in this
// package's apply=false convention.
func TestRemoveMOCDraftDryRunWritesNothing(t *testing.T) {
	v := t.TempDir()
	name := mocDraftFixture(t, v, "gate")
	if e := ReviewMOC(v, name, "process:test", true, true); e != nil {
		t.Fatal(e)
	}
	// ReviewMOC's own apply=true call above already wrote a log entry --
	// capture it so the assertion below is "gained no NEW entry", not "no
	// log file exists at all".
	before, e := os.ReadFile(filepath.Join(v, "99 - Meta/log.md"))
	if e != nil {
		t.Fatal(e)
	}
	if e := RemoveMOCDraft(v, name, "dry run only", false); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(v, "00 - Inbox", name)); e != nil {
		t.Fatalf("dry run removed the draft: %v", e)
	}
	after, e := os.ReadFile(filepath.Join(v, "99 - Meta/log.md"))
	if e != nil {
		t.Fatal(e)
	}
	if string(after) != string(before) {
		t.Fatalf("dry run wrote a log entry:\nbefore: %s\nafter:  %s", before, after)
	}
}
