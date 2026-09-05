// Command sourcecatalogue emits the estate's source catalogue record
// (agent-estate#1139 gate 5): one JSON record per ingestion source, with its
// harness, root path, identity fields, current health state, and last
// observed unit count with the instant it was measured.
//
// This binary is read-only end to end. It never writes to a source root,
// never opens ~/corpus/ledger.sqlite3, and never regenerates the shared
// knowledge index.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jonhill90/agent-estate/estate/internal/catalogue"
)

func main() {
	codexRoot := flag.String("codex-root", "", "override the Codex rollout root (default: ~/.codex/sessions)")
	claudeRoot := flag.String("claude-root", "", "override the Claude transcript root (default: ~/.claude/projects)")
	flag.Parse()

	var cat catalogue.Catalogue
	if *codexRoot == "" && *claudeRoot == "" {
		cat = catalogue.Build()
	} else {
		codex := *codexRoot
		claude := *claudeRoot
		if codex == "" {
			codex = catalogue.DefaultCodexRoot()
		}
		if claude == "" {
			claude = catalogue.DefaultClaudeRoot()
		}
		cat = catalogue.Catalogue{
			Sources: []catalogue.Source{
				catalogue.BuildCodexSource(codex),
				catalogue.BuildClaudeSource(claude),
			},
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cat); err != nil {
		fmt.Fprintf(os.Stderr, "sourcecatalogue: encoding json: %v\n", err)
		os.Exit(1)
	}
}
