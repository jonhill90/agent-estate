package main

// THE WORKLOAD, stated, because a benchmark whose scale is not stated is not
// a measurement.
//
// N turns. Turn i asks the agent to read the i-th of N generated 40-line
// files in its working directory and answer two facts about it in one line.
// Every turn therefore costs one tool call and a few output tokens, and the
// answer is checkable by a human against the file.
//
// WHY A TRIVIAL TASK. What is being priced is the DISPATCH MODE, not the
// work: process startup, prefix caching, and resident memory across turns.
// A heavy task would add variance that belongs to the task and would swamp
// exactly the differences this is trying to see. The cost of that choice is
// stated in the decision record: these are not production-sized turns, and
// the absolute dollar figures are floor values, not typical ones.
//
// WHY THE FILES DIFFER PER TURN. Identical prompts would let the persistent
// arm answer turn 5 from turn 1's conversation without a tool call, which
// would make it look faster and cheaper for a reason that is an artifact of
// the workload rather than a property of persistence.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const workloadLines = 40

func workloadDescription(turns int) string {
	return fmt.Sprintf("%d turns per arm; turn i reads the i-th of %d generated %d-line text files in the working directory and reports its line count and one word from it",
		turns, turns, workloadLines)
}

func workloadPrompts(turns int) []string {
	out := make([]string, 0, turns)
	for i := 1; i <= turns; i++ {
		out = append(out, fmt.Sprintf(
			"Read the file f%02d.txt in this directory. Reply with exactly one line containing the number of lines in the file, a space, and the seventh word on line 7. No other text.", i))
	}
	return out
}

// scratchDir builds one arm's working directory: the workload files, and
// optionally a CLAUDE.md, so that the cached prompt prefix is the shape a
// real dispatched turn carries rather than an empty project's.
//
// Each arm gets its OWN directory, which also gives it its own Claude Code
// project directory and therefore its own transcripts -- the persistent
// arm's session file is then unambiguous without this program having to
// reimplement the CLI's cwd mangling.
func scratchDir(arm string, turns int, ctxFile string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "dispatchbench-"+arm+"-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() {
		os.RemoveAll(dir)
		removeProjectDir(dir)
	}
	for i := 1; i <= turns; i++ {
		var b strings.Builder
		for ln := 1; ln <= workloadLines; ln++ {
			fmt.Fprintf(&b, "file %02d line %02d: alpha bravo charlie delta echo foxtrot golf%02d hotel india juliett\n", i, ln, i*ln)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), []byte(b.String()), 0o644); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	if ctxFile != "" {
		b, err := os.ReadFile(ctxFile)
		if err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("read -context-file: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), b, 0o644); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return dir, cleanup, nil
}

// removeProjectDir deletes the transcripts this run caused Claude Code to
// write under ~/.claude/projects. 176 abandoned worktrees are what unbounded
// per-turn state looks like when nobody removes the first few; a benchmark
// that leaves a project directory per run is the same habit.
//
// It matches on the directory's OWN recorded cwd rather than on a
// reconstruction of the CLI's name mangling, for the same reason
// findTranscript does, and it refuses to remove anything whose transcripts do
// not name this run's scratch directory.
func removeProjectDir(scratch string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dirs, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*"))
	if err != nil {
		return
	}
	for _, d := range dirs {
		files, err := filepath.Glob(filepath.Join(d, "*.jsonl"))
		if err != nil || len(files) == 0 {
			continue
		}
		mine := true
		for _, f := range files {
			if !transcriptIsFor(f, scratch) {
				mine = false
				break
			}
		}
		if mine {
			os.RemoveAll(d)
		}
	}
}
