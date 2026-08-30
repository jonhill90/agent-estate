package skills

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// InvocationsNoHistory is Skill.InvocationState's value when the
// invocation cache was read successfully but has never been built, or was
// built and is genuinely empty -- distinct from InvocationsStoreUnreadable
// (agent-tui#174, the same distinction agent-tui#146 drew for VERDICT via
// VerdictStoreUnreadable, mirrored exactly). "No local history" names the
// fact plainly: this machine has not (yet) built a rollup of its own
// transcripts, not that every skill was checked and used zero times.
const InvocationsNoHistory = "no local history"

// InvocationsStoreUnreadable is Skill.InvocationState's value when the
// cache file exists but could not be read as valid JSON in the expected
// shape -- corrupt, truncated, or wrong-shaped. A skill genuinely invoked
// zero times (a cache that loaded fine and simply has no entry for this
// Dir) must never render this word; see skillInvocationCache's own doc
// comment for the load path that keeps the two apart.
const InvocationsStoreUnreadable = "store unreadable"

// skillTranscriptToolUseName is the tool-call name a Skill invocation
// shows up as in a transcript's own JSONL -- confirmed against a live
// corpus (this file's own doc comment below), not assumed from the tool's
// display name alone.
const skillTranscriptToolUseName = "Skill"

// builtinCommandNames is Claude Code's own documented, product-shipped
// command vocabulary (docs.claude.com/en/docs/claude-code/slash-commands),
// normalised (no leading "/", lowercase, single token). These are never
// filesystem skill directories -- a real Skill.Dir comes from a SKILL.md's
// own directory name under a skills root (Scan), never from the harness's
// built-in command table -- so any `input.skill` value that normalises to
// one of these names is the harness logging a native command through the
// same `Skill` tool_use block, not a skill invocation (agent-estate#890:
// reconciling the cache against `~/.claude/skills` found /model, /model
// sonnet, model, compact, run and loop all counted as if they were skills).
//
// This is a stable, documented set -- not a copy of the specific handful
// of names agent-estate#890 happened to reconcile against -- so a new built-in shipping
// later needs adding here once, not a fresh denylist built from whatever
// the next reconciliation turns up. It is deliberately narrower than "every
// skill this session's Skill tool can reach": agent-estate#890 also found artifact-design,
// career-ops and using-tmux in the cache with no matching directory under
// `~/.claude/skills`, and investigation found career-ops is a genuine
// project-local skill (`.claude/skills/career-ops` in that project's own
// checkout) and using-tmux/artifact-design are real historical invocations
// of skills since renamed or retired -- none of the three is a built-in
// command, so none belongs in this set; excluding them would erase real
// invocation history, not fix the defect.
var builtinCommandNames = map[string]bool{
	"add-dir":           true,
	"agents":            true,
	"bug":               true,
	"clear":             true,
	"compact":           true,
	"config":            true,
	"context":           true,
	"cost":              true,
	"doctor":            true,
	"exit":              true,
	"export":            true,
	"help":              true,
	"hooks":             true,
	"ide":               true,
	"init":              true,
	"login":             true,
	"logout":            true,
	"loop":              true,
	"mcp":               true,
	"memory":            true,
	"migrate-installer": true,
	"model":             true,
	"output-style":      true,
	"permissions":       true,
	"pr-comments":       true,
	"release-notes":     true,
	"resume":            true,
	"review":            true,
	"rewind":            true,
	"run":               true,
	"security-review":   true,
	"status":            true,
	"statusline":        true,
	"terminal-setup":    true,
	"todos":             true,
	"usage":             true,
	"vim":               true,
}

// normalizeSkillInput turns a raw `input.skill` string into the token
// countSkillInvocations checks builtinCommandNames against: a leading "/"
// is always a slash-command spelling (a real Skill.Dir never contains one),
// stripped along with any trailing arguments after the first space -- the
// same collapse "/model" and "/model sonnet" both need to compare equal to
// bare "model" (agent-estate#890 found all three recorded separately for
// the same command). The stripped/lowered result is returned for the
// builtinCommandNames membership check; the ORIGINAL raw string is still
// what gets counted when it is not a built-in, so a real skill's own casing
// or name is never altered on disk.
func normalizeSkillInput(raw string) string {
	s := strings.TrimPrefix(raw, "/")
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// skillInvocationCache is the on-disk shape InvocationCachePath's file is
// written and read as -- a flat map from Skill.Dir to a raw invocation
// count, plus BuiltAt so a reader (or a human) can tell how stale the
// rollup is. Deliberately the smallest shape that answers the column's
// question; no per-session or per-day breakdown, because nothing today
// reads one.
type skillInvocationCache struct {
	BuiltAt string         `json:"built_at"`
	Counts  map[string]int `json:"counts"`
}

// DefaultInvocationCacheDir returns this machine's own per-user state
// directory for the cache InvocationFetcher reads -- $XDG_STATE_HOME if
// set (the convention every other Linux/XDG-aware tool on this box
// follows), else $HOME/.local/state, joined with "estate" -- the shipped
// binary's own name (agent-estate#874: agent-tui was the decommissioned
// repo this package used to live in; "estate" is what a human on this box
// actually runs, the same XDG convention every other tool here follows of
// naming its state directory after its own binary rather than the git
// repo/module path it happens to be built from -- "agent-estate" names the
// repo, not the program). Deliberately OUTSIDE any git checkout:
// agent-tui#164's decision was explicit that this is private, per-machine,
// ephemeral cache, never evidence worth versioning, and never destined for
// jonhill90/skills or agent-evals (skills#272 already had to unwind that
// exact mistake once for eval evidence -- this must not repeat it for
// invocation evidence). Returns an error only if neither $XDG_STATE_HOME
// nor $HOME can be resolved, which InvocationFetcher treats as "cache path
// unavailable" -- a InvocationsStoreUnreadable case, since the caller
// asked for a location and none could be produced, not "checked and
// empty."
func DefaultInvocationCacheDir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "estate"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "estate"), nil
}

// DefaultInvocationCachePath is DefaultInvocationCacheDir joined with this
// cache's own filename.
func DefaultInvocationCachePath() (string, error) {
	dir, err := DefaultInvocationCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "skill-invocations.json"), nil
}

// loadInvocationCache reads path as a skillInvocationCache. Three outcomes,
// each returned distinctly rather than collapsed:
//   - path does not exist: (nil map, nil error, notBuilt=true) -- the
//     cache was never built. Not an error: a fresh checkout with no
//     rollup yet is the expected common case, the same way an unconfigured
//     -skills-repo is for EvalStatusFetcher.
//   - path exists but fails to read or parse: (nil, err, false) -- a real
//     error, the InvocationsStoreUnreadable case.
//   - path exists and parses: (counts map, nil, false) -- even if Counts
//     is an empty map, this is real data (a rollup that ran and found
//     nothing), not the same fact as "never built."
func loadInvocationCache(path string) (map[string]int, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, nil
		}
		return nil, false, err
	}
	var c skillInvocationCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, false, err
	}
	if c.Counts == nil {
		c.Counts = map[string]int{}
	}
	return c.Counts, false, nil
}

// WriteInvocationCache writes counts to path as a skillInvocationCache,
// creating path's parent directory if needed. builtAt is stored verbatim
// (an RFC3339 timestamp is the expected caller convention, but this
// function does not enforce one -- cmd/skillinvocations, the one real
// caller, is what supplies it).
func WriteInvocationCache(path, builtAt string, counts map[string]int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	c := skillInvocationCache{BuiltAt: builtAt, Counts: counts}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// InvocationFetcher wraps base (typically the result of EvalStatusFetcher)
// and merges each Skill's InvocationCount/InvocationState in from
// cachePath's own skillInvocationCache, the same "wrap and merge" shape
// EvalStatusFetcher itself uses over Scan.
//
// cachePath == "" (DefaultInvocationCachePath could not resolve a
// location) is treated as InvocationsStoreUnreadable for every skill --
// there is nowhere to read from, which is a "could not look" fact, not
// "looked and found nothing" (matching loadInvocationCache's own
// not-found-vs-unreadable split, just with one fewer state since there is
// no cache path to even attempt).
func InvocationFetcher(base Fetcher, cachePath string) Fetcher {
	return func() ([]Skill, error) {
		out, err := base()
		if err != nil {
			return nil, err
		}
		if cachePath == "" {
			markInvocationsUnreadable(out)
			return out, nil
		}
		counts, notBuilt, err := loadInvocationCache(cachePath)
		if notBuilt {
			markInvocationsNoHistory(out)
			return out, nil
		}
		if err != nil {
			markInvocationsUnreadable(out)
			return out, nil
		}
		for i := range out {
			n, ok := counts[out[i].Dir]
			if !ok {
				// Cache loaded fine and simply has no entry for this
				// Dir -- a real, counted zero, not "no history": the
				// corpus was scanned and this skill genuinely never
				// appeared in it.
				n = 0
			}
			count := n
			out[i].InvocationCount = &count
			out[i].InvocationState = ""
		}
		return out, nil
	}
}

// markInvocationsNoHistory and markInvocationsUnreadable are
// InvocationFetcher's two "every skill says so" tails, kept as separate
// functions (rather than one with a parameter) so each call site names
// its own fact plainly, matching markStoreUnreadable's own shape in
// evalstatus.go.
func markInvocationsNoHistory(skills []Skill) {
	for i := range skills {
		skills[i].InvocationCount = nil
		skills[i].InvocationState = InvocationsNoHistory
	}
}

func markInvocationsUnreadable(skills []Skill) {
	for i := range skills {
		skills[i].InvocationCount = nil
		skills[i].InvocationState = InvocationsStoreUnreadable
	}
}

// BuildInvocationCache scans transcriptsDir (this machine's own
// ~/.claude/projects, one *.jsonl file per session) for `Skill` tool_use
// blocks and returns a Dir -> count map, keyed by the invoked skill's own
// name (`input.skill`, which matches Skill.Dir for every real skill this
// estate ships -- e.g. "adopt-or-build", "devils-advocate"). A built-in
// command recorded under the same field (observed: "/model", "/model
// sonnet", "model", "compact", "run", "loop") is excluded rather than
// counted under its own literal string -- see builtinCommandNames and
// normalizeSkillInput's own doc comments (agent-estate#890: these six read
// as skills before this fix, three of them the same command spelled three
// ways). Anything else not in builtinCommandNames IS still counted under
// its own literal string with no further filtering, on the same "count
// what was actually asked for" principle Scan applies to SKILL.md
// directories -- including a name with no matching Skill.Dir today, since
// that can be a genuine historical invocation of a skill since renamed,
// retired, or living outside skillsDir (agent-estate#890's own
// investigation: career-ops, using-tmux, artifact-design).
//
// skillsDir, if non-empty, is Scan'd so every skill directory found there
// gets a real, honest 0 entry in the returned map when the corpus never
// invoked it -- countSkillInvocations only increments for a name it
// actually saw, so a skill invoked zero times would otherwise be silently
// absent from the map rather than present at zero (agent-estate#890: the
// cache read as "46 entries, none zero" while omitting five real skills
// entirely, rather than reading 47 with five honest zeros). skillsDir
// failing to scan (not configured, moved, unreadable) leaves counts
// exactly as the transcript scan produced them, the same "partial data
// beats no data" call this function already makes for one bad transcript
// file.
//
// Coverage, measured against the full corpus rather than a sample
// (agent-tui#174's own acceptance requirement, superseding an earlier
// 300-newest-file check): 3,205 transcript files scanned, 0 JSON parse
// errors, 196,289 total tool_use blocks, 166 Skill invocations. Every
// Skill tool_use block's `input` was one of exactly two shapes --
// `{"skill": "<name>"}` (90 occurrences) or `{"args": ..., "skill":
// "<name>"}` (76 occurrences) -- `input.skill` present in both, so no
// second shape needs handling. Positive control: the highest single-file
// Skill count in this same scan was devils-advocate's own frequent-user
// session, which this scan attributed 23 total devils-advocate calls
// across the corpus, confirming the shape produces a real nonzero count
// rather than a scan that silently matches nothing.
func BuildInvocationCache(transcriptsDir, skillsDir string) (map[string]int, error) {
	entries, err := scanTranscriptFiles(transcriptsDir)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, path := range entries {
		if err := countSkillInvocations(path, counts); err != nil {
			// One unreadable/corrupt transcript file does not invalidate
			// the whole rollup -- transcripts are append-only session
			// logs an unrelated crash can truncate mid-write; skip it and
			// keep scanning the rest, the same "partial data beats no
			// data" call Scan makes for a single bad SKILL.md.
			continue
		}
	}
	zeroFillSkillDirs(counts, skillsDir)
	return counts, nil
}

// zeroFillSkillDirs ensures every skill Scan(skillsDir) finds has an entry
// in counts, defaulting an absent one to a real 0 -- see BuildInvocationCache's
// own doc comment for why. A skill already present (invoked at least once)
// is left untouched.
func zeroFillSkillDirs(counts map[string]int, skillsDir string) {
	if skillsDir == "" {
		return
	}
	found, err := Scan(skillsDir)
	if err != nil {
		return
	}
	for _, s := range found {
		if _, ok := counts[s.Dir]; !ok {
			counts[s.Dir] = 0
		}
	}
}

// scanTranscriptFiles walks dir for every *.jsonl file, at any depth --
// ~/.claude/projects nests one directory per project, one file per
// session inside it.
func scanTranscriptFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".jsonl" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// countSkillInvocations reads path line by line (each line one JSON
// event) and increments counts for every `Skill` tool_use block's
// `input.skill`, matching the shape confirmed in BuildInvocationCache's
// own doc comment.
func countSkillInvocations(path string, counts map[string]int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var event transcriptEvent
		if err := json.Unmarshal(line, &event); err != nil {
			// One malformed line (a truncated final write) does not
			// invalidate the rest of the file.
			continue
		}
		for _, block := range event.Message.Content {
			if block.Type != "tool_use" || block.Name != skillTranscriptToolUseName {
				continue
			}
			if block.Input.Skill != "" && !builtinCommandNames[normalizeSkillInput(block.Input.Skill)] {
				counts[block.Input.Skill]++
			}
		}
	}
	return sc.Err()
}

// transcriptEvent decodes only the fields BuildInvocationCache needs from
// one JSONL line of a Claude Code transcript -- everything else (usage,
// diagnostics, requestId, ...) is ignored by omission, the same "decode
// only what this package renders" discipline evalRecord already follows
// for docs/eval-status.json.
type transcriptEvent struct {
	Message struct {
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				Skill string `json:"skill"`
			} `json:"input"`
		} `json:"content"`
	} `json:"message"`
}
