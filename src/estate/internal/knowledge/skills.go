package knowledge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// skillsRegistryRelPath is where this source reads its one committed
// metadata file, relative to repoRoot -- docs/, the same directory
// repoDocsSource already treats as this repository's own written record
// (docs.go), because a skills registry entry is exactly that: a fact the
// operator recorded about the world, not raw material fetched live the
// way github-stars or corpus-items are. JSONL, one record per line,
// matches this repo's own existing convention for an append-friendly,
// diff-friendly committed log (docs/tick-log.jsonl) rather than
// inventing a new format -- and agent-estate#1021's own issue text
// leaves the eventual storage format for the operator's knowledge base
// an open decision; this is this ONE source's own ingest shape, not a
// claim about that larger question.
const skillsRegistryRelPath = "docs/skills-registry.jsonl"

// skillRegistryStatus is the closed set of values skillRegistryEntry.Status
// may hold -- agent-estate#1021's own framing names "evaluation/adoption
// status... including rejected, with the reason" as part of the metadata,
// so this is a validated field, not free text: an entry with a status
// outside this set is a malformed record, not a new kind of skill.
var skillRegistryStatus = map[string]bool{
	// candidate: recorded, not yet decided either way.
	"candidate": true,
	// adopted: the operator chose to install/load it -- ELSEWHERE, by his
	// own decision (`npx skills add`, or by hand). This registry never
	// performs that step; see skillsSource's own doc comment.
	"adopted": true,
	// rejected: the operator evaluated it and said no. Recording this is
	// agent-estate#1021's own explicit point -- it is what stops the same
	// candidate being re-evaluated every few weeks, and StatusReason is
	// where the why lives.
	"rejected": true,
}

// skillRegistryEntry is the schema for one line of
// docs/skills-registry.jsonl -- metadata ABOUT an external skill, never
// the skill's own material. Repo is GitHub "owner/repo" shorthand (the
// same shape starsSource's own FullName already carries, and the shape
// `npx skills add <owner/repo>@<name>` itself addresses by -- see
// skillPermalink), not a URL: this package constructs the one durable,
// openable locator (skills.sh's own catalog page, the address the
// `skills` CLI's own output already points a caller at) rather than
// trusting a hand-typed URL to stay in the same shape across every row.
type skillRegistryEntry struct {
	Name string `json:"name"`
	// Description is one line, the operator's or the skill's own stated
	// purpose -- never rewritten by this package.
	Description string `json:"description"`
	// Triggers are the keywords a caller might actually type that this
	// skill answers -- sourced from the skill's own metadata (a SKILL.md
	// frontmatter's tags, typically), never invented by whoever records
	// the entry.
	Triggers []string `json:"triggers,omitempty"`
	Repo     string   `json:"repo"`
	// Path is the skill's own sub-path within Repo, when Repo hosts more
	// than one skill (agent-estate#1021's own registry is metadata-only:
	// this is where a skill lives, never a copy of what lives there).
	Path string `json:"path,omitempty"`
	// Revision is the upstream commit this entry's Description/Triggers
	// were read against -- a snapshot, never live-verified by this
	// package (see skillsSource's own doc comment on why).
	Revision string `json:"revision,omitempty"`
	// LastCheckedAt is when Revision was last positively observed, RFC
	// 3339. Empty is a real, distinct state ("never checked"), not
	// merely an unset field -- see skillFreshnessLine.
	LastCheckedAt string `json:"last_checked_at,omitempty"`
	Status        string `json:"status"`
	// StatusReason is required in substance for Status=="rejected"
	// (agent-estate#1021's own "rejected, with the reason") -- not
	// enforced as a hard parse failure here (a reason the operator has
	// not yet written is still a real, honestly-recorded candidate/
	// rejected row), but its absence is visible rather than silent: see
	// skillStatusLine.
	StatusReason string `json:"status_reason,omitempty"`
}

// skillsSource reads repoRoot/docs/skills-registry.jsonl -- one Item per
// line. THIS IS A METADATA READER, NOT AN INSTALLER: nowhere in this
// function, or anywhere else in this package, is `npx skills add` or any
// other install/fetch call made. The structural argument for why that is
// sufficient, not merely a convention nobody happens to have broken yet,
// lives in this PR's own body (agent-estate#1021's own framing: "if the
// only thing stopping installation is that no code calls an installer,
// say so explicitly and argue whether that is sufficient"). NO *exec.Cmd,
// NO NETWORK CALL of any kind appears anywhere in this function's own call
// graph -- mechanically pinned, not merely asserted, by
// TestSkillsSourceCallGraphNeverReachesExecOrNet
// (skills_inertness_test.go, agent-estate#1378's review): a static
// call-graph walk from this function, failing if any function reachable
// from it directly calls os/exec, net, or net/http. See that test's own
// doc comment for the scope this pin covers and does not.
//
// A repoRoot that cannot be resolved is one failed source, matching
// repoDocsSource's own handling (docs.go) -- both read a file this same
// checkout is supposed to carry. A registry file that does not exist yet
// is NOT a failure: a freshly-cloned checkout with no skills recorded is
// a legitimate empty state (matching loopsSource's own "directory exists,
// zero .md files" case, generalised to "file absent, zero entries"), not
// a malfunction -- the registry starts empty and grows as the operator
// evaluates skills, which is ongoing operator work this package has no
// part in. A line that fails to parse, or names a Status outside
// skillRegistryStatus, fails the WHOLE source rather than being skipped
// -- unlike github-stars or the corpus, this file is entirely
// hand-curated and committed by this repo's own conventions, so a
// malformed row means the registry itself is broken and needs a fix, not
// live external data this package should shrug off one bad row of.
func skillsSource(repoRoot string) (SourceResult, []Item) {
	res := SourceResult{Name: "skills-registry"}
	if repoRoot == "" {
		res.Reason = "repository root could not be resolved -- no AGENTS.md found above the working directory, and $ESTATE_REPO_ROOT is not set"
		return res, nil
	}
	// A repoRoot that does not exist at all (misconfigured $ESTATE_REPO_ROOT,
	// a checkout that was removed after resolution) is exactly as
	// unresolved as repoRoot=="" above -- checked explicitly, before the
	// registry file's own open below, so a bogus root is never mistaken for
	// "a real checkout with no registry entries yet" (the one case that IS
	// honest-empty; see this function's own doc comment).
	if fi, err := os.Stat(repoRoot); err != nil || !fi.IsDir() {
		res.Reason = fmt.Sprintf("repository root %s could not be resolved", repoRoot)
		return res, nil
	}

	path := filepath.Join(repoRoot, filepath.FromSlash(skillsRegistryRelPath))
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Legitimate empty state -- see doc comment above.
			res.OK = true
			return res, nil
		}
		res.Reason = fmt.Sprintf("cannot read %s: %v", skillsRegistryRelPath, err)
		return res, nil
	}
	defer f.Close()

	var items []Item
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e skillRegistryEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			res.OK = false
			res.Reason = fmt.Sprintf("%s line %d is not valid JSON: %v", skillsRegistryRelPath, lineNo, err)
			return res, nil
		}
		if e.Name == "" || e.Repo == "" {
			res.OK = false
			res.Reason = fmt.Sprintf("%s line %d is missing name or repo", skillsRegistryRelPath, lineNo)
			return res, nil
		}
		if !skillRegistryStatus[e.Status] {
			res.OK = false
			res.Reason = fmt.Sprintf("%s line %d has status %q, want one of candidate/adopted/rejected", skillsRegistryRelPath, lineNo, e.Status)
			return res, nil
		}

		permalink := skillPermalink(e)
		tier1 := e.Name
		if e.Description != "" {
			tier1 = e.Name + " -- " + e.Description
		}
		structural := []string{"skill-registry", "status:" + e.Status}
		publishable, basis := classify("skill-registry")
		items = append(items, Item{
			ID:             itemID(permalink),
			Source:         "skill-registry",
			Permalink:      permalink,
			StructuralTags: structural,
			SynapticTags:   hashtag(e.Triggers),
			Tier1:          truncate(tier1, 200),
			Tier2:          truncate(skillTier2(e), 400),
			Tier3:          skillTier3(e, permalink),
			Publishable:    publishable,
			PublishBasis:   basis,
		})
	}
	if err := sc.Err(); err != nil {
		res.OK = false
		res.Reason = fmt.Sprintf("%s could not be read: %v", skillsRegistryRelPath, err)
		return res, nil
	}

	res.OK = true
	res.Count = len(items)
	return res, items
}

// skillPermalink is skills.sh's own catalog URL for this entry --
// https://skills.sh/<repo>/<name> -- the address the `skills` CLI's own
// `find` output already points a caller at (verified live 2026-09-11:
// `npx skills find excel` printed exactly this shape for every result),
// not a URL this package invented. Built from Repo+Name rather than Repo
// alone: one repo can carry more than one skill (observed the same day --
// `sbroenne/mcp-server-excel` hosts both `excel-mcp` and `excel-cli`), so
// Repo alone would collide two entries onto the same Item.ID (itemID is a
// pure function of Permalink -- see id.go). Name is the field
// `npx skills add <owner/repo>@<name>` itself addresses by, so this is
// also the locator a caller would actually need to act on the entry
// (deliberately, manually, elsewhere -- see skillsSource's own doc
// comment on why this package never calls that installer itself).
func skillPermalink(e skillRegistryEntry) string {
	return "https://skills.sh/" + e.Repo + "/" + e.Name
}

// skillFreshnessLine renders LastCheckedAt as a visible claim either way
// -- agent-estate#1021's own "freshness is the part that will rot"
// section: "an entry that has not been rechecked in N days says so
// rather than presenting its last-known state as fact." This package
// does not compute N (that needs a policy for what counts as stale,
// which is the operator's decision, and it needs live network access to
// RECHECK, which this metadata-only reader deliberately does not have --
// see skillsSource's own doc comment); what it can do without either is
// never let an unset LastCheckedAt disappear into silence the way an
// omitted JSON field otherwise would.
func skillFreshnessLine(e skillRegistryEntry) string {
	if e.LastCheckedAt == "" {
		return "freshness: never checked"
	}
	return "freshness: last checked " + e.LastCheckedAt
}

// skillStatusLine renders Status plus StatusReason when present -- a
// rejected entry with no reason yet recorded still says so plainly
// ("rejected (no reason recorded)") rather than rendering as a bare
// "rejected" a reader might mistake for a reason simply being empty by
// omission.
func skillStatusLine(e skillRegistryEntry) string {
	if e.StatusReason != "" {
		return e.Status + " (" + e.StatusReason + ")"
	}
	if e.Status == "rejected" {
		return e.Status + " (no reason recorded)"
	}
	return e.Status
}

// skillTier2 is one short paragraph -- description, triggers, status and
// freshness, in that order -- matching the "short paragraph of
// additional context" every other source's Tier2 carries (knowledge.go's
// own doc comment on Item.Tier2).
func skillTier2(e skillRegistryEntry) string {
	var parts []string
	if e.Description != "" {
		parts = append(parts, e.Description)
	}
	if len(e.Triggers) > 0 {
		parts = append(parts, "triggers: "+strings.Join(e.Triggers, ", "))
	}
	parts = append(parts, "status: "+skillStatusLine(e))
	parts = append(parts, skillFreshnessLine(e))
	return strings.Join(parts, " -- ")
}

// skillTier3 is the deepest disclosure rung -- every field the entry
// carries, verbatim, plus the GitHub location (never a copy of the
// skill's own material, which this package never fetches at all). This
// is metadata about metadata: the whole record, not a summary of it,
// the same "Tier3 must be genuinely deeper than Tier2" discipline
// agent-estate#1139 established for the other four sources.
func skillTier3(e skillRegistryEntry, permalink string) string {
	var lines []string
	lines = append(lines, "name: "+e.Name)
	if e.Description != "" {
		lines = append(lines, "description: "+e.Description)
	}
	if len(e.Triggers) > 0 {
		lines = append(lines, "triggers: "+strings.Join(e.Triggers, ", "))
	}
	github := "https://github.com/" + e.Repo
	if e.Path != "" {
		github += " (path: " + e.Path + ")"
	}
	lines = append(lines, "upstream: "+github)
	if e.Revision != "" {
		lines = append(lines, "revision: "+e.Revision)
	} else {
		lines = append(lines, "revision: not recorded")
	}
	lines = append(lines, skillFreshnessLine(e))
	lines = append(lines, "status: "+skillStatusLine(e))
	lines = append(lines, "catalog: "+permalink)
	return strings.Join(lines, "\n")
}
