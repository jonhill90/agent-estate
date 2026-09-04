package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// DefaultOutputPath is where Write puts the compiled index absent an
// override -- a cache-shaped location, not the repository, since this
// file is regenerable and disposable (repo_root=clean).
func DefaultOutputPath() (string, error) {
	if p := os.Getenv("ESTATE_KNOWLEDGE_INDEX"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "index.json"), nil
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
