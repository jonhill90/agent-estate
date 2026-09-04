package knowledge

// This file is `estate vault-view`'s own implementation -- a second,
// unrelated render pass over the corpus, separate from Generate/Write's
// compiled index (knowledge.go, write.go) and from Query/Get's read path
// (query.go, which this file does not touch or call). agent-estate#1084:
// the compiled index makes an AGENT better at retrieving the operator's
// knowledge; this makes the OPERATOR able to read it, directly, in his own
// vault, in Obsidian.
//
// WHY A SEPARATE READ, NOT A REUSE OF corpusSource (corpus.go): corpusSource
// deliberately excludes kind=thought (see its own doc comment) and includes
// both weight=hard and weight=preference rows. This view's scope is
// narrower on weight (hard only, agent-estate#1084's own "2,638 hard items"
// framing) and wider on kind (thought included) -- two different filters
// for two different purposes, so a second query is what keeps corpusSource
// itself unchanged rather than growing a parameter that only this caller
// would ever set.
//
// NEVER RAW PROMPTS. Exactly like corpusSource, this reads items.body (an
// already-judged, already-derived item), never prompts.text_raw or
// prompts.text_clean, and carries prompt_id -- the bare id -- as the
// reader's trace back to the operator's own words, never the words
// themselves.
//
// WRITES ONLY TO agent/parameters/, NEVER agent/facts/. vault.go's
// vaultSource reads agent/facts/*.md as one of the compiled index's own
// five sources; writing this view's output there would make this package
// read back its own generated files as if the operator had written them.
// agent/parameters/ is a new, unindexed directory for exactly this reason.

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// VaultViewConfig is every input GenerateVaultView needs, each overridable
// so a test never touches the real vault, the real corpus, or the real
// backup location.
type VaultViewConfig struct {
	VaultDir     string // $AGENT_MEMORY_VAULT
	CorpusDBPath string // ~/corpus/ledger.sqlite3 by default
	BackupRoot   string // where BackupVault writes its timestamped copies
}

// DefaultVaultViewConfig resolves the same VaultDir and CorpusDBPath
// DefaultConfig (write.go) already resolves for the compiled index --
// including agent-estate#942's own trap named there -- plus BackupRoot, a
// location this command alone owns. $ESTATE_VAULT_BACKUP_ROOT overrides the
// default the same way $ESTATE_KNOWLEDGE_INDEX overrides
// DefaultOutputPath (write.go) -- so an end-to-end CLI test never has to
// touch the operator's real ~/.local/state/estate/vault-backups to exercise
// the real binary.
func DefaultVaultViewConfig() (VaultViewConfig, error) {
	cfg, err := DefaultConfig()
	if err != nil {
		return VaultViewConfig{}, err
	}
	backupRoot := os.Getenv("ESTATE_VAULT_BACKUP_ROOT")
	if backupRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return VaultViewConfig{}, err
		}
		backupRoot = filepath.Join(home, ".local", "state", "estate", "vault-backups")
	}
	return VaultViewConfig{
		VaultDir:     cfg.VaultDir,
		CorpusDBPath: cfg.CorpusDBPath,
		BackupRoot:   backupRoot,
	}, nil
}

// VaultViewResult is what one GenerateVaultView call actually did --
// printed by `estate vault-view` (main.go) rather than recomputed there.
type VaultViewResult struct {
	BackupPath string
	OutputDir  string
	Files      []string
	Counts     map[string]int
	Total      int
}

// vaultViewKinds fixes both the file this view writes for each corpus kind
// and the order those files are listed in index.md -- a plain literal, not
// derived from corpusKinds (corpus.go), because this view's kind set is
// different (see this file's own doc comment: thought is included here,
// excluded there).
var vaultViewKinds = []struct {
	Kind, File, Title string
}{
	{"directive", "directives.md", "Directives"},
	{"parameter", "parameters.md", "Parameters"},
	{"correction", "corrections.md", "Corrections"},
	{"question", "questions.md", "Questions he asked"},
	{"thought", "thoughts.md", "Thoughts"},
}

// hardItem is one row of the corpus's own `items` table at weight='hard' --
// this file's own read shape, deliberately not knowledge.Item (Match/Get's
// compiled-index shape carries fields, like Publishable, that have no
// meaning for a view written straight into the operator's own vault).
type hardItem struct {
	ID, Kind, Weight, Status, ResolvedTo, Body, PromptID string
}

const hardItemSep = "\x1f"

// readHardItems reads every items row at weight='hard', across all five
// corpus kinds, ordered by kind then id -- ordered so two calls against an
// unchanged corpus return rows in the same order, which GenerateVaultView's
// idempotency depends on.
func readHardItems(dbPath string) ([]hardItem, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("no corpus path configured")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("corpus unreadable at %s: %w", dbPath, err)
	}
	q := `select id, kind, weight, status, coalesce(resolved_to,''),
	      replace(replace(body, char(10), ' '), char(13), ' '), coalesce(prompt_id,'')
	      from items where weight='hard' order by kind, id`
	cmd := exec.Command("sqlite3", "-separator", hardItemSep, "file:"+dbPath+"?mode=ro&immutable=1", q)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("corpus query failed: %w", err)
	}

	var items []hardItem
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), hardItemSep, 7)
		if len(parts) != 7 {
			continue
		}
		items = append(items, hardItem{
			ID:         parts[0],
			Kind:       parts[1],
			Weight:     parts[2],
			Status:     parts[3],
			ResolvedTo: parts[4],
			Body:       strings.TrimSpace(parts[5]),
			PromptID:   parts[6],
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("corpus output could not be read: %w", err)
	}
	return items, nil
}

// GenerateVaultView backs up the vault, reads every hard item from the
// corpus, and writes one Markdown file per kind plus an index into
// cfg.VaultDir/agent/parameters/ -- refusing (no vault write at all)
// whenever the vault cannot be found, the backup cannot be made, or the
// corpus cannot be read, so a failure never leaves a half-written or
// stale-looking view sitting in the operator's own vault.
func GenerateVaultView(cfg VaultViewConfig, now time.Time) (VaultViewResult, error) {
	if cfg.VaultDir == "" {
		return VaultViewResult{}, fmt.Errorf("$AGENT_MEMORY_VAULT is not set -- refusing to write a view with no vault to write it into")
	}
	if _, err := os.Stat(cfg.VaultDir); err != nil {
		return VaultViewResult{}, fmt.Errorf("vault unreadable at %s: %w", cfg.VaultDir, err)
	}

	// Backup happens before ANY write below, every run -- not a one-time
	// manual step this code trusts someone remembered. agent-estate#1084
	// requires a backup "before the first write"; backing up before every
	// write is the strict superset of that and needs no separate
	// "have I already done this once" state.
	backupPath, err := backupVault(cfg.VaultDir, cfg.BackupRoot, now)
	if err != nil {
		return VaultViewResult{}, fmt.Errorf("vault backup failed, refusing to write: %w", err)
	}

	items, err := readHardItems(cfg.CorpusDBPath)
	if err != nil {
		return VaultViewResult{}, fmt.Errorf("cannot read hard items from corpus, refusing to write: %w", err)
	}

	byKind := map[string][]hardItem{}
	for _, it := range items {
		byKind[it.Kind] = append(byKind[it.Kind], it)
	}

	outDir := filepath.Join(cfg.VaultDir, "agent", "parameters")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return VaultViewResult{}, fmt.Errorf("cannot create %s: %w", outDir, err)
	}

	res := VaultViewResult{BackupPath: backupPath, OutputDir: outDir, Counts: map[string]int{}}
	for _, k := range vaultViewKinds {
		kindItems := byKind[k.Kind]
		res.Counts[k.Kind] = len(kindItems)
		res.Total += len(kindItems)
		content := renderKindFile(k.Kind, k.Title, kindItems)
		path := filepath.Join(outDir, k.File)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return VaultViewResult{}, fmt.Errorf("cannot write %s: %w", path, err)
		}
		res.Files = append(res.Files, path)
	}

	indexPath := filepath.Join(outDir, "index.md")
	if err := os.WriteFile(indexPath, []byte(renderIndexFile(res.Counts, res.Total)), 0o644); err != nil {
		return VaultViewResult{}, fmt.Errorf("cannot write %s: %w", indexPath, err)
	}
	res.Files = append(res.Files, indexPath)
	sort.Strings(res.Files)

	return res, nil
}

// derivedVaultNote is this view's own "GENERATED, do not hand-edit" line --
// the same discipline derivedNote (knowledge.go) states for the compiled
// index, restated here in this view's own words per agent-estate#1084's
// explicit requirement that the generated file say so itself.
func derivedVaultNote(kind string) string {
	return fmt.Sprintf("GENERATED by `estate vault-view`. This is a derived, regenerable "+
		"view over the operator's own corpus (kind=%s AND weight='hard') -- it is not, "+
		"and must never be treated as, the authoritative home for this data, and it is "+
		"not itself a knowledge source (nothing under agent/parameters/ is read back by "+
		"`estate knowledge`). Do not hand-edit this file; regenerate it with "+
		"`estate vault-view` instead.\n", kind)
}

const questionBanner = "\n**These are questions he asked, recorded at hard weight -- not " +
	"decisions he made.** A question filed at hard weight reached retrieval once already " +
	"as if it were settled law (agent-estate#1051); conflating the two here would repeat " +
	"that. Read every entry below as \"he asked this\", never as \"he decided this.\"\n"

// renderKindFile is one kind's own Markdown file -- items already filtered
// to weight='hard' and this one kind by the caller (GenerateVaultView).
//
// NO WALL-CLOCK TEXT IN CONTENT. Earlier drafts embedded a "Generated: "
// timestamp line here; agent-estate#1084 requires "running twice produces
// byte-identical output. Prove it," and two real runs of `estate
// vault-view` a moment apart over an UNCHANGED corpus must therefore
// produce identical bytes, not merely identical modulo one line. Freshness
// is still observable -- the file's own filesystem mtime, exactly the
// signal indexSourceMtime (main.go) already reads elsewhere in this repo
// rather than trusting embedded prose.
func renderKindFile(kind, title string, items []hardItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (weight=hard)\n\n", title)
	b.WriteString(derivedVaultNote(kind))
	if kind == "question" {
		b.WriteString(questionBanner)
	}
	fmt.Fprintf(&b, "\nItem count: %d\n\n---\n\n", len(items))

	if len(items) == 0 {
		b.WriteString("_no hard-weight items of this kind as of this generation._\n")
		return b.String()
	}

	for _, it := range items {
		heading := it.ResolvedTo
		if heading == "" {
			heading = truncate(it.Body, 120)
		}
		if heading == "" {
			heading = it.ID
		}
		fmt.Fprintf(&b, "## %s\n\n", heading)
		if kind == "question" {
			b.WriteString("QUESTION -- not settled law.\n\n")
		}
		fmt.Fprintf(&b, "- kind: %s\n- id: %s\n- prompt_id: %s\n- status: %s\n- weight: hard\n\n",
			it.Kind, it.ID, it.PromptID, it.Status)
		if it.Body != "" {
			fmt.Fprintf(&b, "%s\n\n", it.Body)
		}
		b.WriteString("---\n\n")
	}
	return b.String()
}

// renderIndexFile is agent/parameters/index.md -- one table, one link per
// kind, and the same "he asked vs. he decided" distinction stated up front
// so a reader sees it before ever opening questions.md itself. No embedded
// wall-clock text, for the same reason renderKindFile has none -- see its
// own doc comment.
func renderIndexFile(counts map[string]int, total int) string {
	var b strings.Builder
	b.WriteString("# Operator parameters -- hard items (index)\n\n")
	b.WriteString("GENERATED by `estate vault-view`. This is a derived, regenerable view " +
		"over the operator's own corpus (~/corpus/ledger.sqlite3's `items` table, " +
		"weight='hard' across every kind) -- it is not, and must never be treated as, " +
		"the authoritative home for this data, and it is not itself a knowledge source. " +
		"Do not hand-edit this file; regenerate it with `estate vault-view` instead.\n\n")
	b.WriteString("**A question filed at hard weight is not a decision.** questions.md is " +
		"marked distinctly from directives.md, parameters.md and corrections.md for " +
		"exactly this reason -- see agent-estate#1051.\n\n")
	fmt.Fprintf(&b, "Total hard items: %d\n\n", total)
	b.WriteString("| kind | count | file |\n|---|---|---|\n")
	for _, k := range vaultViewKinds {
		fmt.Fprintf(&b, "| %s | %d | [%s](./%s) |\n", k.Kind, counts[k.Kind], k.File, k.File)
	}
	return b.String()
}

// backupVault copies vaultDir, recursively and in full, to a new
// timestamped directory under backupRoot -- BEFORE GenerateVaultView writes
// anything, on every run, per agent-estate#1084's own requirement. now is
// injected (never time.Now() read directly here) for the same reason every
// other timestamp in this package is injected: a test drives this
// deterministically, and this package never calls the wall clock itself.
func backupVault(vaultDir, backupRoot string, now time.Time) (string, error) {
	if backupRoot == "" {
		return "", fmt.Errorf("no backup root configured")
	}
	dest := filepath.Join(backupRoot, "vault-"+now.UTC().Format("20060102T150405Z"))
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("backup destination %s already exists, refusing to overwrite a prior backup", dest)
	}
	if err := copyTree(vaultDir, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// copyTree copies src to dst, file by file, preserving the tree shape --
// no external `cp`/`rsync`/`tar` dependency, matching this package's
// existing sqlite3-CLI-only, otherwise-dependency-free posture (see
// corpus.go, stars.go).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// A symlink inside the vault (none expected, but never
			// followed if one exists) is skipped rather than copied as a
			// dangling or mis-resolved link in the backup.
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
