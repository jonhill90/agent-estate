package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jonhill90/agent-estate/estate/internal/isolate"
)

// DefaultConfig resolves every source path the same way estate's other
// commands resolve theirs -- an env var override first, a fixed default
// second, agent-estate#942's own trap (CLAUDE.md documenting the wrong
// corpus path) named explicitly so nobody re-introduces it here.
func DefaultConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		VaultDir:      os.Getenv("AGENT_MEMORY_VAULT"),
		LoopsResearch: filepath.Join(home, "source", "repos", "Personal", "Loops-Research"),
	}
	if p := os.Getenv("ESTATE_CORPUS"); p != "" {
		cfg.CorpusDBPath = p
	} else {
		// NOT ~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3 --
		// that path has zero live_parameters (agent-estate#942). The real
		// corpus is ~/corpus/ledger.sqlite3.
		cfg.CorpusDBPath = filepath.Join(home, "corpus", "ledger.sqlite3")
	}
	if p := os.Getenv("ESTATE_REPO_ROOT"); p != "" {
		cfg.RepoRoot = p
	} else if wd, err := os.Getwd(); err == nil {
		cfg.RepoRoot = findRepoRoot(wd) // "" if no AGENTS.md found above wd
	}
	return cfg, nil
}

// findRepoRoot walks upward from start looking for a directory
// containing AGENTS.md -- the same marker-file convention `git` itself
// uses for `.git`, so `estate knowledge` resolves the same repo root
// regardless of whether it is invoked from the repo root or from a
// subdirectory such as src/estate. Returns "" (never an error) if no
// AGENTS.md is found within maxRepoRootDepth levels -- repoDocsSource
// (docs.go) treats that as one failed source, the same honest-absence
// path every other source in this package already uses, not a fatal
// DefaultConfig error.
const maxRepoRootDepth = 12

func findRepoRoot(start string) string {
	dir := start
	for i := 0; i < maxRepoRootDepth; i++ {
		if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached filesystem root without finding AGENTS.md
		}
		dir = parent
	}
	return ""
}

// DefaultOutputPath is where the compiled index is READ from absent an
// override -- a cache-shaped location, not the repository, since this file
// is regenerable and disposable (repo_root=clean).
//
// agent-estate#1048: every dispatched turn used to resolve the SAME shared
// path here, so two lanes running `estate knowledge` concurrently -- or a
// lane and the Director -- silently overwrote each other's index and each
// other's measurements. internal/isolate already gives each dispatched turn
// its own git worktree for exactly this class of problem (see that
// package's own doc comment); this is the same argument applied to the
// index. A turn running inside a worktree isolate.Create/CreateOnBranch
// made (isolate.IsDispatchWorktree, keyed off the process's own working
// directory) gets a path namespaced by its own dispatch id, so no two turns
// can resolve the same default. A caller that is not inside one of those
// worktrees -- the operator at a terminal, the Director -- sees no change:
// IsDispatchWorktree reports false and the shared default below is
// returned exactly as before.
//
// The explicit ESTATE_KNOWLEDGE_INDEX override, checked first, is
// unaffected either way -- a turn or a reviewer that sets it by hand still
// gets exactly that path, per agent-estate#1048's own "the workaround
// should be the default, not a thing careful reviewers remember" -- taking
// the override away would break the measurements that already depend on it.
//
// This function is safe to leave exactly as-is for READS (query, get):
// falling through to the shared path there costs nothing, because reading
// it cannot corrupt it. agent-estate#1184 found the fall-through IS a
// problem for the one caller that WRITES through this same resolution --
// see ResolveWritePath below, which is what that caller must use instead.
func DefaultOutputPath() (string, error) {
	path, _, err := resolveOutputPath()
	return path, err
}

// resolveOutputPath is the resolution DefaultOutputPath and ResolveWritePath
// both build on. sharedFallback reports whether path is the shared default
// reached by falling through both the explicit override and the
// dispatch-worktree check -- the one case agent-estate#1184 found unsafe to
// hand to a writer without the caller saying so on purpose.
func resolveOutputPath() (path string, sharedFallback bool, err error) {
	if p := os.Getenv("ESTATE_KNOWLEDGE_INDEX"); p != "" {
		return p, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	if wd, err := os.Getwd(); err == nil {
		if id, ok := isolate.IsDispatchWorktree(wd); ok {
			return filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "dispatch", id, "index.json"), false, nil
		}
	}
	return filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "index.json"), true, nil
}

// AllowSharedWriteEnv is the explicit acknowledgement required to write the
// shared index from a cwd that resolveOutputPath cannot place: neither an
// explicit ESTATE_KNOWLEDGE_INDEX override nor a recognised dispatch
// worktree (isolate.IsDispatchWorktree).
const AllowSharedWriteEnv = "ESTATE_KNOWLEDGE_ALLOW_SHARED_WRITE"

// ResolveWritePath is DefaultOutputPath's counterpart for the one caller
// that actually WRITES the compiled index (`estate knowledge`'s
// regeneration path in main.go) rather than reading it back (query, get).
//
// agent-estate#1184: a reviewer created an isolated checkout the ordinary,
// correct way -- `git worktree add /tmp/...` -- which is a real git
// worktree but not one isolate.Create/CreateOnBranch made, so
// IsDispatchWorktree reported false exactly as it must (widening it to
// accept arbitrary temp paths would make every ad-hoc clone look like a
// dispatched turn, which agent-estate#1184 rules out explicitly). Nothing
// else stood between that "false" and DefaultOutputPath's shared-index
// fallback, so an ordinary, correct review action silently overwrote the
// one index every other lane reads, seeded from a PR branch rather than
// main, with no prompt or record.
//
// This is fix shape 1 of the three the issue lays out (make the shared
// write opt-in), chosen over shape 2 (refuse when the cwd LOOKS like an
// ad-hoc isolated checkout) and shape 3 (warn and proceed):
//
//   - Shape 3 is out because this estate has already named the failure mode
//     directly: "a tool that fails closed and that nothing calls is a
//     documentation rule with a binary attached" applies just as much to a
//     guard that reports but never refuses. The #1184 reviewer already
//     noticed and disclosed the overwrite on their own, unprompted, with no
//     warning to help them -- a warning proves nothing here that did not
//     already happen without one.
//   - Shape 2 requires a classifier that can tell "the operator, at a
//     terminal, in their own checkout" apart from "an agent in an ad-hoc
//     worktree" from the cwd alone. Both false directions are worse than
//     today: false-positive blocks Jon's own terminal, false-negative is
//     exactly the silent write this issue exists to stop, and #1184 says so
//     explicitly ("getting it wrong in either direction is worse than the
//     status quo"). No such signal exists in the cwd shape today, and
//     inventing one risks being confidently wrong rather than honestly
//     unable to tell.
//   - Shape 1 needs no classifier at all: every cwd that is not an explicit
//     override and not a real dispatch worktree is treated identically,
//     which is exactly the set #1184 found unsafe. The cost is real and is
//     named in the doc comment on purpose, not left to be discovered later:
//     regenerating the shared index by hand -- something Jon legitimately
//     does -- now requires this acknowledgement too. See main.go's
//     `--allow-shared-write` flag (or setting AllowSharedWriteEnv directly)
//     for how an operator opts back in.
func ResolveWritePath() (path string, requiresAck bool, err error) {
	path, shared, err := resolveOutputPath()
	if err != nil {
		return "", false, err
	}
	if shared && os.Getenv(AllowSharedWriteEnv) == "" {
		return path, true, nil
	}
	return path, false, nil
}

// Write serializes res as indented JSON to path, creating its parent
// directory if needed. This is the ONLY write this whole package
// performs -- to its own output path, never to any of the five sources.
func Write(path string, res Result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

// Read reads back a previously-written Result, e.g. for the TUI's own
// compiled-index pane -- a plain read of this package's own artifact,
// never a second writer of it.
func Read(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		return Result{}, fmt.Errorf("%s is not a valid compiled index: %w", path, err)
	}
	return res, nil
}
