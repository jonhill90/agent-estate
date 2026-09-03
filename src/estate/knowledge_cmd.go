package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// starsSource reads the operator's GitHub stars.
//
// A star is already a judgement: dated, deduplicated, and needing no
// distillation. That is why it is the first source indexed -- and why the
// index records what it CANNOT know, which is why each was starred. The
// stars carry no note, so the index says so rather than implying the row is
// the whole story.
func starsSource(run func(string, ...string) ([]byte, error), now time.Time) (knowledge.Source, error) {
	out, err := run("gh", "api", "user/starred", "--paginate",
		"-q", `.[] | [.full_name, .pushed_at, (.description // "")] | @tsv`)
	if err != nil {
		return knowledge.Source{}, fmt.Errorf("cannot read the stars: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	var rows [][]string
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		rows = append(rows, strings.SplitN(ln, "\t", 3))
	}
	ids := knowledge.NewIDs(now, len(rows))

	src := knowledge.Source{
		Slug: "github-stars", Name: "GitHub stars",
		Origin: "gh api user/starred --paginate",
		Note: "A star is a judgement that needed no distillation: dated, deduplicated, and made\n" +
			"deliberately. What it does not carry is WHY. Until a reaction tag is attached, each\n" +
			"row says only that this was worth keeping — not what it was for.",
	}
	for i, r := range rows {
		full := r[0]
		pushed, detail := "", ""
		if len(r) > 1 {
			pushed = r[1]
		}
		if len(r) > 2 {
			detail = r[2]
		}
		if len(detail) > 140 {
			detail = detail[:137] + "…"
		}
		signal := "starred"
		if pushed != "" {
			signal = "pushed " + pushed[:10]
		}
		it := knowledge.Item{
			ID: ids[i], Title: full, Detail: detail,
			Permalink:  "https://github.com/" + full,
			Signal:     signal,
			Structural: []string{"stars"},
		}
		// Synaptic tags express association. These are derived from what the
		// repository says about itself -- never invented, and never a guess
		// at why he starred it.
		low := strings.ToLower(full + " " + detail)
		for tag, words := range map[string][]string{
			"agent-harness": {"agent", "claude", "codex", "acp"},
			"knowledge":     {"knowledge", "memory", "obsidian", "graph", "notes"},
			"terminal-ui":   {"tui", "terminal", "bubbletea", "ratatui"},
		} {
			for _, w := range words {
				if strings.Contains(low, w) {
					it.Synaptic = append(it.Synaptic, tag)
					break
				}
			}
		}
		src.Items = append(src.Items, it)
	}
	return src, nil
}

// writeKnowledge compiles the index to disk. Every file is overwritten
// wholesale: this artifact is regenerated, never merged into.
func writeKnowledge(root string, sources []knowledge.Source, now time.Time) error {
	dir := filepath.Join(root, "docs", "knowledge")
	if err := os.MkdirAll(filepath.Join(dir, "sources"), 0o755); err != nil {
		return err
	}
	for _, s := range sources {
		p := filepath.Join(dir, "sources", s.Slug+".md")
		if err := os.WriteFile(p, []byte(knowledge.SourceIndex(s, now)), 0o644); err != nil {
			return err
		}
		fmt.Printf("  %s (%d items)\n", p, len(s.Items))
	}
	p := filepath.Join(dir, "index.md")
	if err := os.WriteFile(p, []byte(knowledge.TopIndex(sources, now)), 0o644); err != nil {
		return err
	}
	fmt.Printf("  %s\n", p)
	return nil
}

func runKnowledge() {
	now := time.Now().UTC()
	run := func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	}
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "estate: cannot find the repository root:", err)
		os.Exit(2)
	}
	root := strings.TrimSpace(string(top))

	stars, err := starsSource(run, now)
	if err != nil {
		// A source that cannot be read is reported, never written as empty:
		// an index claiming zero stars would be a lie with a timestamp on it.
		fmt.Fprintln(os.Stderr, "estate:", err)
		os.Exit(1)
	}
	fmt.Printf("compiling knowledge index at %s\n", now.Format(time.RFC3339))
	if err := writeKnowledge(root, []knowledge.Source{stars}, now); err != nil {
		fmt.Fprintln(os.Stderr, "estate:", err)
		os.Exit(2)
	}
	fmt.Println("derived and regenerable -- delete it and nothing is lost")
}
