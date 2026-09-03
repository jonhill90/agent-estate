package main

import (
	"os"
	"path/filepath"
)

// compiledIndexPath resolves where `estate knowledge`
// (src/estate/internal/knowledge, a DIFFERENT Go module -- this repo has
// never imported that module, see internal/knowledgeindex's own package
// doc comment on why) writes its compiled index. ESTATE_KNOWLEDGE_INDEX
// overrides, matching that command's own env var name exactly; otherwise
// this is that command's own DefaultOutputPath, duplicated here rather
// than imported since the two are separate binaries in separate modules.
// A path that resolves to nothing yet (nobody has run `estate knowledge`
// this machine) is not an error at THIS call site -- internal/knowledge's
// own [c] pane surfaces that honestly the first time it is opened.
func compiledIndexPath() string {
	if p := os.Getenv("ESTATE_KNOWLEDGE_INDEX"); p != "" {
		return p
	}
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "index.json")
}
