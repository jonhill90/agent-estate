// Command sourcecatalogue emits the estate's source catalogue record
// (agent-estate#1139 gate 5): one JSON record per ingestion source, with its
// harness, root path, identity fields, current health state, and last
// observed unit count with the instant it was measured.
//
// This binary is read-only end to end for the health report. Its four
// registration subcommands (register/list/show/refresh, added for the
// 2026-09-06 knowledge-architecture run's lane B) are the only thing
// that ever writes, and only ever to the private register under
// ~/.local/state/agent-estate/catalogue -- never to a source root, never
// to ~/corpus/corpus.sqlite3, never to the shared `estate knowledge`
// index.
//
// With no subcommand at all, this binary's behavior is exactly what it
// was before these subcommands existed: the health report described
// above, unchanged.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/candidates"
	"github.com/jonhill90/agent-estate/estate/internal/catalogue"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "register":
			runRegister(os.Args[2:])
			return
		case "list":
			runList(os.Args[2:])
			return
		case "show":
			runShow(os.Args[2:])
			return
		case "refresh":
			runRefresh(os.Args[2:])
			return
		}
	}
	runHealth(os.Args[1:])
}

// runHealth is the original, unchanged no-arg invocation.
func runHealth(args []string) {
	fs := flag.NewFlagSet("sourcecatalogue", flag.ExitOnError)
	codexRoot := fs.String("codex-root", "", "override the Codex rollout root (default: ~/.codex/sessions)")
	claudeRoot := fs.String("claude-root", "", "override the Claude transcript root (default: ~/.claude/projects)")
	fs.Parse(args)

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
		sources := []catalogue.Source{
			catalogue.BuildCodexSource(codex),
			catalogue.BuildClaudeSource(claude),
		}
		for _, d := range catalogue.SeedPDFDescriptors {
			sources = append(sources, catalogue.BuildSeedPDFSource(d))
		}
		cat = catalogue.Catalogue{Sources: sources}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cat); err != nil {
		fmt.Fprintf(os.Stderr, "sourcecatalogue: encoding json: %v\n", err)
		os.Exit(1)
	}
}

// registerDirFlag adds the -register-dir flag every subcommand below
// shares, defaulting to catalogue.DefaultRegisterDir().
func registerDirFlag(fs *flag.FlagSet) *string {
	def, err := catalogue.DefaultRegisterDir()
	if err != nil {
		def = ""
	}
	return fs.String("register-dir", def, "the private register's directory (default: ~/.local/state/agent-estate/catalogue, or $ESTATE_CATALOGUE_REGISTER)")
}

func loadOrExit(dir string) *catalogue.Register {
	reg, err := catalogue.LoadRegister(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sourcecatalogue: loading register at %s: %v\n", dir, err)
		os.Exit(1)
	}
	return reg
}

func saveOrExit(dir string, reg *catalogue.Register) {
	if err := catalogue.SaveRegister(dir, reg); err != nil {
		fmt.Fprintf(os.Stderr, "sourcecatalogue: saving register at %s: %v\n", dir, err)
		os.Exit(1)
	}
}

func writeViewsOrExit(entries []catalogue.RegisterEntry, viewsDir string) {
	if viewsDir == "" {
		return
	}
	n, err := catalogue.WriteViewsStaging(entries, viewsDir, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sourcecatalogue: writing views to %s: %v\n", viewsDir, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "sourcecatalogue: wrote %d source view(s) to %s\n", n, viewsDir)
}

func printEntry(e catalogue.RegisterEntry, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(e)
		return
	}
	fmt.Printf("%s\t%s\t%s\t%s\n", e.ID, e.Kind, e.Status, e.Locator)
}

// contractEnumFlag validates value against allowed (a closed contract.md
// §1 axis) unless value is empty (a default will be applied downstream) --
// exits with a clear, named error rather than silently registering an
// entry contract.md would reject.
func contractEnumFlag(flagName, value string, allowed map[string]bool, allowedList string) {
	if value == "" {
		return
	}
	if !allowed[value] {
		fmt.Fprintf(os.Stderr, "sourcecatalogue register: -%s %q is not one of the contract's closed values: %s\n", flagName, value, allowedList)
		os.Exit(2)
	}
}

func runRegister(args []string) {
	fs := flag.NewFlagSet("sourcecatalogue register", flag.ExitOnError)
	registerDir := registerDirFlag(fs)
	viewsDir := fs.String("views-dir", "", "explicit destination root for backed-up generated source views and area index")
	extractionKind := fs.String("extraction-kind", "", "extraction mechanism: pdf, conversation, repo-docs, or repo-pointer (others register with extraction marked unavailable)")
	kind := fs.String("kind", "", "contract.md content-type tag: one of kind/repo, kind/doc, kind/transcript, kind/decision, kind/skill, kind/tool")
	locator := fs.String("locator", "", "the file or root path this source lives at")
	provenance := fs.String("provenance", "", "who or what produced this RECORD (not the original) -- e.g. an agent id or 'operator, via sourcecatalogue'")
	attribution := fs.String("attribution", "", "who or what authored the ORIGINAL this record points at; defaults to \"unknown\" if unset, never left blank")
	authority := fs.String("authority", "", "how much weight this source's content should carry against a conflicting claim")
	scope := fs.String("scope", "", "what this source does and does not cover")
	access := fs.String("access", "", "contract.md access axis: public, private, or scoped (defaults to private)")
	accessDetail := fs.String("access-detail", "", "free-text elaboration of -access, e.g. a specific handling constraint")
	owner := fs.String("owner", "", "who is accountable for this source's registration being accurate")
	freshness := fs.String("freshness", "", "how current this source's content is")
	reviewState := fs.String("review-state", "", "contract.md lifecycle axis: lifecycle/candidate, lifecycle/current, lifecycle/superseded, lifecycle/rejected (defaults to lifecycle/candidate)")
	whyIndexed := fs.String("why-indexed", "", "why this source is worth cataloguing")
	links := fs.String("links", "", "comma-separated links to reviewed derivatives of this source")
	remoteURL := fs.String("remote-url", "", "repo-pointer only (P8): the canonical GitHub URL, kept separate from -local-path and never derived from it")
	localPath := fs.String("local-path", "", "repo-pointer only (P8): a local checkout path; absent/nonexistent is recorded as such, never an error, never guessed from -remote-url")
	repoDescription := fs.String("repo-description", "", "repo-pointer only (P8): what this repo is, one line, drawn from its own README")
	routingSurface := fs.String("routing-surface", "", "repo-pointer only (P8): where this repo's own routing surface lives, e.g. AGENTS.md or docs/index.md")
	asJSON := fs.Bool("json", false, "print the registered entry as JSON")
	fs.Parse(args)

	if *extractionKind == "" || *locator == "" {
		fmt.Fprintln(os.Stderr, "sourcecatalogue register: -extraction-kind and -locator are required")
		os.Exit(2)
	}
	contractEnumFlag("kind", *kind, catalogue.ContractKinds, "kind/repo, kind/doc, kind/transcript, kind/decision, kind/skill, kind/tool")
	contractEnumFlag("access", *access, catalogue.ContractAccessLevels, "public, private, scoped")
	contractEnumFlag("review-state", *reviewState, catalogue.ContractReviewStates, "lifecycle/candidate, lifecycle/current, lifecycle/superseded, lifecycle/rejected")

	var derivativeLinks []string
	if *links != "" {
		derivativeLinks = strings.Split(*links, ",")
	}

	reg := loadOrExit(*registerDir)
	entry, created := reg.Register(*registerDir, catalogue.RegisterInput{
		Kind:            *kind,
		ExtractionKind:  catalogue.ExtractionKind(*extractionKind),
		Locator:         *locator,
		Provenance:      *provenance,
		Attribution:     *attribution,
		Authority:       *authority,
		Scope:           *scope,
		Access:          *access,
		AccessDetail:    *accessDetail,
		Owner:           *owner,
		Freshness:       *freshness,
		ReviewState:     *reviewState,
		WhyIndexed:      *whyIndexed,
		DerivativeLinks: derivativeLinks,
		RemoteURL:       *remoteURL,
		LocalPath:       *localPath,
		RepoDescription: *repoDescription,
		RoutingSurface:  *routingSurface,
	}, time.Now())
	saveOrExit(*registerDir, reg)
	writeViewsOrExit(reg.List(), *viewsDir)

	if created {
		fmt.Fprintf(os.Stderr, "sourcecatalogue: registered new entry %s\n", entry.ID)
	} else {
		fmt.Fprintf(os.Stderr, "sourcecatalogue: updated existing entry %s (idempotent -- no duplicate created)\n", entry.ID)
	}
	printEntry(entry, *asJSON)
}

func runList(args []string) {
	fs := flag.NewFlagSet("sourcecatalogue list", flag.ExitOnError)
	registerDir := registerDirFlag(fs)
	asJSON := fs.Bool("json", false, "print every entry as a JSON array")
	fs.Parse(args)

	reg := loadOrExit(*registerDir)
	entries := reg.List()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(entries)
		return
	}
	for _, e := range entries {
		printEntry(e, false)
	}
}

func runShow(args []string) {
	fs := flag.NewFlagSet("sourcecatalogue show", flag.ExitOnError)
	registerDir := registerDirFlag(fs)
	asJSON := fs.Bool("json", false, "print the entry as JSON")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: sourcecatalogue show [-register-dir DIR] [-json] <id>")
		os.Exit(2)
	}

	reg := loadOrExit(*registerDir)
	entry, ok := reg.Show(fs.Arg(0))
	if !ok {
		fmt.Fprintf(os.Stderr, "sourcecatalogue: no entry %q in register at %s\n", fs.Arg(0), *registerDir)
		os.Exit(1)
	}
	printEntry(entry, *asJSON)
}

func runRefresh(args []string) {
	fs := flag.NewFlagSet("sourcecatalogue refresh", flag.ExitOnError)
	vault := fs.String("vault", "", "explicit INMAPS vault: mark source-dependent notes needs_review with backups")
	registerDir := registerDirFlag(fs)
	viewsDir := fs.String("views-dir", "", "explicit destination root for backed-up generated source views and area index")
	all := fs.Bool("all", false, "refresh every entry in the register")
	ack := fs.Bool("ack", false, "acknowledge a needs_review entry back to active, without a revision change (requires exactly one id, not -all)")
	asJSON := fs.Bool("json", false, "print the refreshed entry/entries as JSON")
	fs.Parse(args)

	reg := loadOrExit(*registerDir)

	if *ack {
		if fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "usage: sourcecatalogue refresh -ack <id>")
			os.Exit(2)
		}
		entry, ok := reg.Acknowledge(fs.Arg(0), time.Now())
		if !ok {
			fmt.Fprintf(os.Stderr, "sourcecatalogue: no entry %q in register\n", fs.Arg(0))
			os.Exit(1)
		}
		saveOrExit(*registerDir, reg)
		printEntry(entry, *asJSON)
		return
	}

	var ids []string
	if *all {
		for _, e := range reg.List() {
			ids = append(ids, e.ID)
		}
	} else {
		if fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "usage: sourcecatalogue refresh [-all] <id>")
			os.Exit(2)
		}
		ids = []string{fs.Arg(0)}
	}

	var refreshed []catalogue.RegisterEntry
	for _, id := range ids {
		entry, ok := reg.Refresh(*registerDir, id, time.Now())
		if !ok {
			fmt.Fprintf(os.Stderr, "sourcecatalogue: no entry %q in register\n", id)
			os.Exit(1)
		}
		refreshed = append(refreshed, entry)
		if entry.Status == catalogue.StatusNeedsReview {
			if *vault != "" {
				if _, err := candidates.MarkSourceDrift(*vault, entry.ID); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			}
			fmt.Fprintf(os.Stderr, "sourcecatalogue: %s flipped to needs_review -- content changed since last review\n", entry.ID)
		}
	}
	saveOrExit(*registerDir, reg)
	writeViewsOrExit(reg.List(), *viewsDir)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(refreshed)
		return
	}
	for _, e := range refreshed {
		printEntry(e, false)
	}
}
