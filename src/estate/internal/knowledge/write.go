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
	return cfg, nil
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
// performs -- to its own output path, never to any of the four sources.
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
