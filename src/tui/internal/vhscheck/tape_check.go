// Package vhscheck is the durable guard agent-tui#132 asked for: a
// mechanical check that a VHS tape's own `go build ... ./cmd/<name>`
// reference names a directory that actually exists.
//
// Why this exists: three tapes (agents-mode.tape, agent-tui#130;
// knowledge.tape and knowledge-route.tape, agent-tui#132) each referenced
// a cmd/ path that had either never existed or been quietly removed, and
// nothing caught any of the three until a human swept every tape by hand.
// `go build` failing inside a tape produces no screenshot and no error
// anyone reads -- the visual QA the tape represents silently stops
// happening, indistinguishable from visual QA that ran and passed. This
// package is the check that makes that failure loud instead: it runs as
// an ordinary `go test`, already gated on every PR (.github/workflows/
// ci.yml's own `go test ./...` step), so a broken reference fails CI the
// same day it's introduced rather than waiting for the next by-hand sweep.
//
// Deliberately narrow, matching the issue's own scope: this checks that
// the REFERENCED DIRECTORY EXISTS, nothing about whether the tape itself
// still runs correctly (that is what actually running vhs proves, by
// hand, per tape -- a much larger and flakier surface, see
// agents-mode.tape's own header comment on vhs's own capture-pipeline
// flake). A missing directory is a mechanical fact a `go build` inside
// the tape would hit every single time; that is the class of failure
// worth gating CI on.
package vhscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// cmdRefPattern matches a `go build`-shaped line's own build target,
// `./cmd/<name>` (or `cmd/<name>` with no leading `./`) -- deliberately
// anchored to the same LINE as the literal text "go build", not to any
// occurrence of "cmd/<name>" anywhere in the file. A tape's own header
// comment may legitimately MENTION a sibling tape's build target in
// prose (knowledge-route.tape's own history did exactly this, describing
// knowledge.tape's target) without that being a real reference this
// tape's own execution depends on -- scanning every line for the bare
// substring would have flagged that prose forever, including after the
// referenced file no longer exists to explain it. Requiring "go build"
// on the same line is what a real build/run dependency actually looks
// like in every tape this repo has (`git grep -n 'go build' testdata/vhs`
// confirms one line, one build target, every time, whether the line is a
// live `Type "go build ..."` command or a `#`-prefixed comment
// documenting a prerequisite command the operator runs first, e.g.
// chat-send.tape's own `cmd/fakemcp` -- both are real dependencies and
// both are meant to be caught).
var cmdRefPattern = regexp.MustCompile(`go build[^\n]*?\.?/cmd/([A-Za-z0-9_-]+)`)

// Reference is one `.tape` file's own dependency on a `cmd/<Name>`
// package, as found by ScanTapes.
type Reference struct {
	TapePath string // repo-relative, e.g. "testdata/vhs/knowledge.tape"
	CmdName  string // e.g. "knowledgedemo"
	Line     int    // 1-based line number the reference was found on
}

// CmdDir is the repo-relative directory a Reference's CmdName resolves
// to, e.g. "cmd/knowledgedemo" -- exported so a caller can report it
// without re-deriving the join.
func (r Reference) CmdDir() string {
	return filepath.Join("cmd", r.CmdName)
}

// ScanTapes walks every `.tape` file directly under tapesDir (and its
// subdirectories, e.g. testdata/vhs/variants/) and returns every
// cmd/<name> reference found, in a stable, deterministic order (by tape
// path, then by line number) -- so a test asserting on the returned slice
// never flakes on directory-walk ordering.
func ScanTapes(tapesDir string) ([]Reference, error) {
	var refs []Reference
	err := filepath.WalkDir(tapesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tape") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(filepath.Dir(tapesDir), path)
		if err != nil {
			relPath = path
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "go build") {
				continue
			}
			m := cmdRefPattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			refs = append(refs, Reference{TapePath: relPath, CmdName: m[1], Line: i + 1})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].TapePath != refs[j].TapePath {
			return refs[i].TapePath < refs[j].TapePath
		}
		return refs[i].Line < refs[j].Line
	})
	return refs, nil
}

// MissingCmdDirs returns every Reference in refs whose CmdDir() does not
// exist under repoRoot, in the same order ScanTapes returned them. An
// empty, non-nil-vs-nil-agnostic result means every reference resolved --
// callers should check len(...) == 0, not nil-ness.
func MissingCmdDirs(repoRoot string, refs []Reference) []Reference {
	var missing []Reference
	for _, ref := range refs {
		info, err := os.Stat(filepath.Join(repoRoot, ref.CmdDir()))
		if err != nil || !info.IsDir() {
			missing = append(missing, ref)
		}
	}
	return missing
}

// agent-estate#754: twelve tapes hardcoded
// `${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}`
// -- a literal filesystem path outside the cmdRefPattern's scope entirely,
// so it went stale the day that directory was renamed to agent-estate and
// nothing above caught it. This is the same failure SHAPE as agent-tui#132
// (a hardcoded reference to a path that doesn't exist) on a DIFFERENT kind
// of line -- an `export VAR=${VAR:-default}` default, not a `go build`
// target -- so it needs its own scan and its own existence check rather
// than a widened cmdRefPattern.

// exportPattern matches a `Type "export VAR=${VAR:-default}" Enter` line
// and captures the variable name and its literal default text. Anchored to
// this exact `${VAR:-...}` shape -- the one every tape in this repo
// actually uses (`git grep -n 'Type "export' testdata/vhs` confirms it,
// same way cmdRefPattern's own comment justifies its anchor) -- rather than
// any bare `export` occurrence, so a comment mentioning "export" in prose
// is never mistaken for a live dependency.
var exportPattern = regexp.MustCompile(`Type "export ([A-Za-z_][A-Za-z0-9_]*)=\$\{[A-Za-z_][A-Za-z0-9_]*:-([^}]*)\}" Enter`)

// PathExport is one `export VAR=${VAR:-default}` line whose default is a
// literal filesystem path -- as opposed to a derived one, see IsDerived.
type PathExport struct {
	TapePath string // repo-relative, e.g. "vhs/agents.tape"
	VarName  string // e.g. "AGENT_SUPERVISOR_REPO"
	Default  string // the raw default text, e.g. "$HOME/source/repos/Personal/hill90-app"
	Line     int    // 1-based line number
}

// IsDerived reports whether Default is computed by the shell (contains
// `$(`) rather than written as a literal path. A derived default -- the
// fix agent-estate#754 applied, deriving AGENT_SUPERVISOR_REPO from `git
// rev-parse --show-toplevel` instead of a hardcoded sibling-checkout path
// -- has nothing static to check: its value depends on where vhs is run
// from, which is exactly the "much larger and flakier surface" this
// package's own header says it stays out of. Only literal defaults are
// checkable without running a shell.
func (p PathExport) IsDerived() bool {
	return strings.Contains(p.Default, "$(")
}

// looksLikeAPath reports whether a default value is shaped like a
// filesystem path at all, so a default like "8080" or "true" (a plausible
// shape for some future non-path export) is never scanned as one. A
// derived (`$(...)`) default counts too -- it is still a path-shaped
// export, just not a checkable one; ScanExportedPaths returns it and
// MissingExportedPaths is the one that skips it via IsDerived, so a
// caller inspecting ScanExportedPaths's own output can still see it.
func looksLikeAPath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "$HOME") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.Contains(s, "$(")
}

// ScanExportedPaths walks every `.tape` file directly under tapesDir (and
// its subdirectories) and returns every `export VAR=${VAR:-default}` line
// whose default is shaped like a literal filesystem path, in the same
// stable order ScanTapes uses. A derived default (IsDerived) is still
// returned -- callers that only care about checkable literals filter on
// IsDerived themselves, the same way MissingExportedPaths does.
func ScanExportedPaths(tapesDir string) ([]PathExport, error) {
	var exports []PathExport
	err := filepath.WalkDir(tapesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tape") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(filepath.Dir(tapesDir), path)
		if err != nil {
			relPath = path
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "Type \"export") {
				continue
			}
			m := exportPattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			def := strings.TrimSpace(m[2])
			if !looksLikeAPath(def) {
				continue
			}
			exports = append(exports, PathExport{TapePath: relPath, VarName: m[1], Default: def, Line: i + 1})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(exports, func(i, j int) bool {
		if exports[i].TapePath != exports[j].TapePath {
			return exports[i].TapePath < exports[j].TapePath
		}
		return exports[i].Line < exports[j].Line
	})
	return exports, nil
}

// resolvePathExport turns a PathExport's literal Default into an absolute
// path, resolving a `$HOME`/`~` prefix against home (the caller's own
// os.UserHomeDir(), passed in rather than read here so a test never
// depends on the machine actually running it) and a bare relative path
// against repoRoot -- the same "resolve against a known root, never guess"
// shape MissingCmdDirs already uses for cmd/<name>.
func resolvePathExport(repoRoot, home, def string) string {
	switch {
	case strings.HasPrefix(def, "$HOME"):
		return filepath.Join(home, strings.TrimPrefix(def, "$HOME"))
	case strings.HasPrefix(def, "~"):
		return filepath.Join(home, strings.TrimPrefix(def, "~"))
	case filepath.IsAbs(def):
		return def
	default:
		return filepath.Join(repoRoot, def)
	}
}

// MissingExportedPaths returns every literal PathExport in exports whose
// resolved Default does not exist on disk, skipping:
//
//   - derived defaults (IsDerived) -- nothing static to check, see its doc
//   - any VarName present in optionalVars -- a named, explicit allowlist,
//     not a guess (Invariant 6's own "whitelist, not a guess" applied
//     here): HILL90_APP_REPO is the one live case, and it belongs on this
//     list on the evidence of its own downstream code, not by assumption --
//     internal/apidocs.resolveOpenAPISpec and internal/secrets.
//     resolveSecretsSchema both have their own tests
//     (TestResolveOpenAPISpecKeepsAWrongRepoPath-shaped) proving an absent
//     or wrong HILL90_APP_REPO is a supported, rendered "not configured"
//     state, not a hard failure the way a stale AGENT_SUPERVISOR_REPO is
//     (a wrong mcp_server.py path fails the whole session, not one pane).
//     CI's own tui-ci.yml confirms the same asymmetry operationally: it
//     sets AGENT_SUPERVISOR_REPO explicitly and never sets HILL90_APP_REPO
//     at all, so hill90-app's default is *expected* to be absent there.
//     A future optional sibling-repo export earns the same exemption by
//     being added here explicitly, with the same kind of citation --
//     never by widening looksLikeAPath or resolvePathExport to stop seeing
//     it.
//
// home is the resolved value for a `$HOME`-prefixed default; pass "" if it
// could not be determined (os.UserHomeDir() failing) and every such export
// is skipped rather than falsely flagged -- "could not measure" is a real
// outcome here, not a guess.
func MissingExportedPaths(repoRoot, home string, exports []PathExport, optionalVars map[string]bool) []PathExport {
	var missing []PathExport
	for _, exp := range exports {
		if exp.IsDerived() || optionalVars[exp.VarName] {
			continue
		}
		if strings.HasPrefix(exp.Default, "$HOME") || strings.HasPrefix(exp.Default, "~") {
			if home == "" {
				continue
			}
		}
		resolved := resolvePathExport(repoRoot, home, exp.Default)
		if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
			missing = append(missing, exp)
		}
	}
	return missing
}

// agent-estate#761: two tapes (skill-invocations.tape, skills-eval-store.tape)
// launched a binary against `/tmp` fixtures (`HOME=/tmp/...`,
// `-skills-repo=/tmp/...`) that the tape itself never created -- a
// different failure shape from both checks above (neither a `go build`
// target nor an `export VAR=${VAR:-default}` line), but the same root
// cause class MissingExportedPaths was written for: a literal filesystem
// path a tape depends on that nothing keeps honest. Unlike the other two
// checks, this one cannot verify against the LIVE filesystem -- a /tmp
// fixture is built by the tape's own Hide block when vhs actually runs,
// not present at `go test` time -- so this is a purely textual check: does
// the tape's own content contain a creation command (`mkdir -p` naming the
// path, or a `>`/`>>` redirect writing it directly) for every `/tmp` path
// it references as a launch argument. That mirrors exactly the diagnostic
// the issue itself used to find both defects (`grep -cE "mkdir|cat
// >|printf.*>" ...`), made structural instead of a one-off grep.

// tmpFixtureRefPattern matches a `KEY=/tmp/...` token inside a `Type
// "..." Enter` line -- KEY is either a bare shell var (`HOME=`) or a CLI
// flag (`-skills-repo=`). This is deliberately the shape a LAUNCH line
// has, not a creation line: `mkdir -p /tmp/x` and `printf ... >
// /tmp/x` have no `KEY=` immediately before the path, so this pattern
// never matches a creation line by construction -- no separate
// creation-vs-launch line classification is needed.
var tmpFixtureRefPattern = regexp.MustCompile(`(?:^|[\s"])([A-Za-z_][A-Za-z0-9_]*=|-[A-Za-z][A-Za-z0-9-]*=)(/tmp/[A-Za-z0-9_./-]+)`)

// tmpMkdirPattern captures a `mkdir`/`mkdir -p` line's own space-separated
// path arguments, up to a shell operator (`&&`) or the end of the string
// (VHS's own closing `"`) -- multiple directories in one `mkdir -p a b c`
// call, exactly the shape both fixed tapes use, are all captured together
// and split by the caller.
var tmpMkdirPattern = regexp.MustCompile(`mkdir\s+(?:-p\s+)?([^&"]+)`)

// tmpRedirectPattern captures a `>`/`>>` shell redirect's target path --
// `printf ... > /tmp/x.json`, the shape both fixed tapes use to write a
// fixture file's content directly.
var tmpRedirectPattern = regexp.MustCompile(`>>?\s*(/tmp/[A-Za-z0-9_./-]+)`)

// TmpFixtureRef is one `/tmp` path a tape references as a launch
// argument, as found by ScanTmpFixtureRefs.
type TmpFixtureRef struct {
	TapePath string // repo-relative, e.g. "vhs/skill-invocations.tape"
	Key      string // e.g. "HOME=" or "-skills-repo="
	Path     string // e.g. "/tmp/atui174-fakehome"
	Line     int    // 1-based line number
}

// ScanTmpFixtureRefs walks every `.tape` file directly under tapesDir (and
// its subdirectories) and returns every `/tmp` path referenced via
// tmpFixtureRefPattern, in the same stable, deterministic order the other
// two scanners use.
func ScanTmpFixtureRefs(tapesDir string) ([]TmpFixtureRef, error) {
	var refs []TmpFixtureRef
	err := filepath.WalkDir(tapesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tape") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(filepath.Dir(tapesDir), path)
		if err != nil {
			relPath = path
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "Type \"") || !strings.Contains(line, "/tmp/") {
				continue
			}
			for _, m := range tmpFixtureRefPattern.FindAllStringSubmatch(line, -1) {
				refs = append(refs, TmpFixtureRef{TapePath: relPath, Key: m[1], Path: m[2], Line: i + 1})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].TapePath != refs[j].TapePath {
			return refs[i].TapePath < refs[j].TapePath
		}
		if refs[i].Line != refs[j].Line {
			return refs[i].Line < refs[j].Line
		}
		return refs[i].Path < refs[j].Path
	})
	return refs, nil
}

// tapeCreatesTmpPath reports whether tapeContent contains a creation
// command (mkdir -p naming path or a directory containing it, or a
// redirect writing path directly) for path -- purely textual, matching
// this check's own doc comment on why it cannot use a live filesystem
// stat the way MissingCmdDirs/MissingExportedPaths do.
func tapeCreatesTmpPath(tapeContent, path string) bool {
	for _, m := range tmpMkdirPattern.FindAllStringSubmatch(tapeContent, -1) {
		for _, dir := range strings.Fields(m[1]) {
			dir = strings.TrimSuffix(dir, `"`)
			// Either direction counts: `mkdir -p path/deeper` implicitly
			// creates path itself as an ancestor (skill-invocations.tape's
			// own shape -- HOME=/tmp/atui174-fakehome is never mkdir'd on
			// its own, only .claude/skills/<dir> beneath it), and `mkdir -p
			// path` directly, or `mkdir -p path` with a referenced
			// subpath beneath it, both also count.
			if dir == path || strings.HasPrefix(path, dir+"/") || strings.HasPrefix(dir, path+"/") {
				return true
			}
		}
	}
	for _, m := range tmpRedirectPattern.FindAllStringSubmatch(tapeContent, -1) {
		if m[1] == path {
			return true
		}
	}
	return false
}

// optionalTmpFixtureKeys is the named, explicit allowlist for a `/tmp`
// path a tape references BUT deliberately never creates -- the same
// "named allowlist, not a guess" shape optionalTapeExportVars uses for
// HILL90_APP_REPO. `-skill-invocations-cache=` is the one live case:
// skill-invocations.tape's own header documents its first render state
// (InvocationsNoHistory) as requiring a cache path that does NOT exist
// (skill.go's loadInvocationCache, the "path does not exist" branch) --
// flagging that absence would be flagging the tape's own correct,
// documented behaviour, not a defect. A future flag earns the same
// exemption only with the same kind of citation to the code path that
// makes its absence a supported state, never by widening
// tmpFixtureRefPattern or tapeCreatesTmpPath to stop seeing it.
var optionalTmpFixtureKeys = map[string]bool{
	"-skill-invocations-cache=": true,
	// AGENT_TUI_ESTATE_BIN=: pressure.tape (agent-estate#987) deliberately
	// references two /tmp paths under this one key -- /tmp/fake-estate-ok,
	// which it does create (a real `estate pressure` stand-in, for the
	// Present/OK capture), and /tmp/estate-does-not-exist, which it must
	// NOT create: that path's whole purpose is to exercise Home's
	// Unreadable rendering for a missing `estate` binary (estatus.
	// ReadPressure's fail-closed path). The check is keyed per-Key, not
	// per-Path, so it cannot tell the two apart; flagging the key here
	// exempts the deliberately-absent one without weakening the check for
	// any other tape.
	"AGENT_TUI_ESTATE_BIN=": true,
}

// MissingTmpFixtures returns every TmpFixtureRef in refs whose Path is not
// created anywhere in its own tape's content, skipping any ref whose Key
// is in optionalKeys. tapeContents maps a ref's TapePath (as ScanTmpFixtureRefs
// returned it) to that tape's full raw content, so this never re-reads the
// filesystem itself -- callers that already have the content (e.g. from
// building refs) can reuse it directly.
func MissingTmpFixtures(refs []TmpFixtureRef, tapeContents map[string]string, optionalKeys map[string]bool) []TmpFixtureRef {
	var missing []TmpFixtureRef
	for _, ref := range refs {
		if optionalKeys[ref.Key] {
			continue
		}
		if !tapeCreatesTmpPath(tapeContents[ref.TapePath], ref.Path) {
			missing = append(missing, ref)
		}
	}
	return missing
}

// LoadTapeContents reads every relPath in refs' own TapePath set from
// tapesDir's parent (relPath is already tapesDir-relative-to-its-own-parent,
// matching ScanTapes/ScanTmpFixtureRefs's own relPath convention) and
// returns a TapePath -> content map for MissingTmpFixtures.
func LoadTapeContents(tapesDir string, refs []TmpFixtureRef) (map[string]string, error) {
	out := map[string]string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref.TapePath] {
			continue
		}
		seen[ref.TapePath] = true
		data, err := os.ReadFile(filepath.Join(filepath.Dir(tapesDir), ref.TapePath))
		if err != nil {
			return nil, err
		}
		out[ref.TapePath] = string(data)
	}
	return out, nil
}
