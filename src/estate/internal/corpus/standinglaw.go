package corpus

// Standing law (agent-estate#1255) is the sole deliberate exception to
// agent-estate#1254's rule: "published Agent Memory / derived knowledge
// content must never enter Hard()/Grounding() as law." #1254 forbids
// ACCIDENTAL promotion -- a heuristic, a tag, a score, a future "make the
// agent aware of accepted facts" change wiring the vault into the preamble
// by inference. It does not, and cannot, forbid a DECLARED promotion: the
// K3 gate (#1255 itself) failed because a genuinely cross-task binding
// fact sat in the vault as evidence a fresh agent had to think to ask for,
// and it never asked. Some constraints must bind whether or not a task's
// own wording happens to surface them.
//
// The reconciliation is this file: StandingLawSet is a human-reviewed,
// explicitly named list -- membership is declared, never inferred, never
// scored, never derived from a vault tag like "important" or "type:
// constraint". A fact is standing law because a human wrote its slug into
// StandingLawSet below and gave a reason, and for no other cause. Every
// other vault fact, every derived-knowledge item, every candidate-queue
// row stays retrieved-only, exactly as #1254 requires -- StandingLaw below
// resolves ONLY the declared members and nothing else that happens to live
// under the same vault directory.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StandingLawMember is one declared entry. All three fields are required:
// an undeclared reason means the membership bar was never actually
// applied, and an unpinned hash means a body could drift after review
// without anyone noticing.
type StandingLawMember struct {
	// Slug is the vault fact's filename stem under agent/facts/ (no .md).
	Slug string
	// HashPrefix is a prefix of the sha256 hex digest of the fact file's
	// bytes as they stood at the moment this member was declared. Checked
	// before the body is trusted -- a vault file that changed since
	// review no longer matches what a human actually read when they
	// promoted it, so resolution refuses rather than injecting
	// silently-drifted text as if it were still the reviewed version.
	HashPrefix string
	// Reason is the one-line, human-reviewed justification for why this
	// specific fact binds every task, not only the task it happened to
	// be published from. Never inferred -- if you cannot write this
	// sentence, the fact does not belong in this set.
	Reason string
}

// StandingLawSet is the declared, capped membership. Nothing is added
// here by heuristic, tag, or score. Keep it small: every member is read,
// unconditionally, by every dispatched turn, whether or not that turn's
// task touches the member's domain.
var StandingLawSet = []StandingLawMember{
	{
		Slug:       "estate-is-skills-practice-not-a-product-to-sell",
		HashPrefix: "a255964bbdcf",
		Reason: "Cross-task by construction: it changes what ANY agent should " +
			"ever recommend for the estate, on any task, not just tasks that " +
			"mention selling. Never propose pricing, packaging, go-to-market, or " +
			"user acquisition, and never justify a technical choice on salability " +
			"grounds.",
	},
}

// MaxStandingLawMembers and MaxStandingLawBytes are hard caps enforced in
// code. Standing law is unconditional -- every dispatched turn carries the
// whole declared set or none of it -- so unlike Grounding's own
// maxMatches/maxPreambleBytes (which truncate a task-matched subset and
// SAY so), there is no partial-render fallback here: exceeding either cap
// is a refusal, never a silent truncation of which members get read.
//
// 5 members / 4096 bytes: enough headroom for a handful of genuinely
// cross-task constraints, small enough that a human reviewer can read the
// entire declared set in one sitting and that it can never approach
// corpus.Hard()'s own 16KB preamble budget. These bound growth of a
// deliberately short list; they are not a target to grow into. Today's
// set holds 1 member.
//
// Vars, not consts, purely so tests can lower them to prove the caps are
// load-bearing and then restore the originals -- production always runs
// with the values below (mirrors corpus.go's maxMatches/maxPreambleBytes/
// maxItemBytes pattern for the identical reason).
var (
	MaxStandingLawMembers = 5
	MaxStandingLawBytes   = 4096
)

// StandingLawEntry is one resolved, ready-to-render standing-law fact.
type StandingLawEntry struct {
	Slug   string
	Reason string
	Body   string
}

// StandingLawAttributionInstruction is the fixed sentence Grounding() prints
// alongside the standing-law section, agent-estate#1255's own re-measurement
// (the K3 gate's second run went 2-of-3: the miss was not disobedience, it
// was an agent that followed an injected member without citing it, so the
// output alone could not distinguish "obeyed the law" from "happened to
// agree with it"). Attribution is made part of the contract here rather than
// left as a hoped-for behaviour.
//
// This is fixed preamble text, not a member body, but it is rendered only
// alongside the standing-law section and exists purely because that section
// exists -- so it counts against MaxStandingLawBytes exactly like a member's
// body would (see StandingLaw() below). If a future member's body no longer
// leaves room for this sentence, that is a finding to report (shrink the
// member, or the set), never a reason to raise the cap.
const StandingLawAttributionInstruction = "When a standing-law member below shapes your answer, say which one by name and state that it came from standing law -- an answer that merely agrees with it, uncited, is not distinguishable from luck and does not satisfy this requirement."

// StandingLaw resolves StandingLawSet against the vault at vaultDir,
// enforcing the member-count cap, the hash-drift check, and the total
// byte cap, in that order. A declared member that fails to resolve is an
// error, never a silent drop: a human said this fact is law, and a
// dispatch that proceeds one binding constraint short of what was
// declared is exactly the failure mode this file exists to close.
//
// vaultDir == "" with an empty StandingLawSet returns (nil, nil) -- no
// vault, nothing declared, nothing to resolve. vaultDir == "" with a
// non-empty StandingLawSet is an error: the set declares law that cannot
// be read, which must stop a dispatch the same way an unreadable corpus
// does in Hard().
func StandingLaw(vaultDir string) ([]StandingLawEntry, error) {
	if len(StandingLawSet) > MaxStandingLawMembers {
		return nil, fmt.Errorf(
			"standing-law set has %d member(s), exceeding the cap of %d -- shrink StandingLawSet before dispatching",
			len(StandingLawSet), MaxStandingLawMembers)
	}
	if vaultDir == "" {
		if len(StandingLawSet) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"standing-law set declares %d member(s) but no vault is configured ($AGENT_MEMORY_VAULT unset) -- cannot resolve declared law",
			len(StandingLawSet))
	}

	var entries []StandingLawEntry
	total := 0
	for _, m := range StandingLawSet {
		if strings.TrimSpace(m.Slug) == "" {
			return nil, fmt.Errorf("standing-law member has no slug")
		}
		if strings.TrimSpace(m.Reason) == "" {
			return nil, fmt.Errorf("standing-law member %q has no stated reason -- membership requires one", m.Slug)
		}
		path := filepath.Join(vaultDir, "agent", "facts", m.Slug+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("standing-law member %q could not be read at %s: %w", m.Slug, path, err)
		}
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if m.HashPrefix == "" || !strings.HasPrefix(got, m.HashPrefix) {
			return nil, fmt.Errorf(
				"standing-law member %q has drifted from what was declared (file hash %s does not start with declared prefix %q) -- re-review before trusting it as law",
				m.Slug, got[:12], m.HashPrefix)
		}
		body := vaultFactBody(string(raw))
		total += len(body)
		if total > MaxStandingLawBytes {
			return nil, fmt.Errorf(
				"standing-law set exceeds the %d-byte cap once %q is included -- shrink a member's body or the set",
				MaxStandingLawBytes, m.Slug)
		}
		entries = append(entries, StandingLawEntry{Slug: m.Slug, Reason: m.Reason, Body: body})
	}
	if len(entries) > 0 {
		total += len(StandingLawAttributionInstruction)
		if total > MaxStandingLawBytes {
			return nil, fmt.Errorf(
				"standing-law set plus its fixed attribution instruction (%d bytes) exceeds the %d-byte cap -- shrink a member's body or the set",
				len(StandingLawAttributionInstruction), MaxStandingLawBytes)
		}
	}
	return entries, nil
}

// vaultFactBody strips a vault fact file down to the text after its
// closing frontmatter fence -- the fact's own substance. A file with no
// opening/closing fence is returned trimmed but otherwise untouched: an
// unparseable file still has to pass the hash check above to be trusted
// at all, so there is no silent-content-loss path through this function.
func vaultFactBody(raw string) string {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return strings.TrimSpace(raw)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return strings.TrimSpace(raw)
}
