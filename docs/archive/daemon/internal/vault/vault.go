// Package vault reads Jon's Obsidian memory vault -- the canonical store.
//
// WHY: his global rules say durable memory lives in $AGENT_MEMORY_VAULT and
// that "Harness-native memory (e.g. Claude Code auto-memory) is project-local
// and does NOT count". Every session so far wrote to the harness-native store
// anyway. The daemon does not have that option: this is the only memory reader
// it has, and every dispatch loads the index before the brief so the agent
// starts with what the estate already knows instead of re-deriving it.
//
// That re-derivation is the measured failure: 98.9% of 84 billion tokens were
// cache reads, and self-review independence was "fixed" 19 separate times by
// 19 sessions each meeting it as if it were new.
package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Vault struct{ Root string }

// Open resolves the vault from $AGENT_MEMORY_VAULT and verifies the index
// exists. A missing vault is an ERROR, never a silently empty memory -- an
// agent that starts blind must know it started blind.
func Open() (*Vault, error) {
	root := os.Getenv("AGENT_MEMORY_VAULT")
	if root == "" {
		return nil, fmt.Errorf("vault: $AGENT_MEMORY_VAULT is unset -- refusing to start blind")
	}
	idx := filepath.Join(root, "agent", "index.md")
	if _, err := os.Stat(idx); err != nil {
		return nil, fmt.Errorf("vault: no index at %s: %w", idx, err)
	}
	return &Vault{Root: root}, nil
}

// Index is the capped, one-line-per-fact file read at session start.
func (v *Vault) Index() (string, error) {
	b, err := os.ReadFile(filepath.Join(v.Root, "agent", "index.md"))
	if err != nil {
		return "", fmt.Errorf("vault: read index: %w", err)
	}
	return string(b), nil
}

// Facts lists fact slugs. Facts load on demand -- that is the progressive
// disclosure the vault already implements; the daemon does not flatten it.
func (v *Vault) Facts() ([]string, error) {
	dir := filepath.Join(v.Root, "agent", "facts")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("vault: read facts: %w", err)
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Fact reads one fact by slug.
func (v *Vault) Fact(slug string) (string, error) {
	b, err := os.ReadFile(filepath.Join(v.Root, "agent", "facts", slug+".md"))
	if err != nil {
		return "", fmt.Errorf("vault: read fact %s: %w", slug, err)
	}
	return string(b), nil
}

// Preamble is prepended to every brief. This is the wiring that was missing:
// memory is not a document an agent may choose to read, it is in the prompt.
func (v *Vault) Preamble() (string, error) {
	idx, err := v.Index()
	if err != nil {
		return "", err
	}
	facts, err := v.Facts()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# What this estate already knows\n\n")
	b.WriteString("This is the memory index. It was LOADED FOR YOU -- you are not\n")
	b.WriteString("starting blind and you must not re-derive what is already recorded here.\n")
	b.WriteString("Facts load on demand from agent/facts/<slug>.md.\n\n")
	b.WriteString(idx)
	fmt.Fprintf(&b, "\n\n(%d facts available)\n\n---\n\n", len(facts))
	return b.String(), nil
}
