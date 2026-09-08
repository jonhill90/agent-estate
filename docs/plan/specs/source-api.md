# Lane B — `internal/catalogue` source-reference API

Frozen for Lane C to code against (`run/execution-plan.md` sequence step 2).
This is the exported Go surface, not an implementation guide — Lane C calls
these functions and reads these types; it does not reach into unexported
helpers (`observe`, `driftStatus`, `cacheDir`, `identityFor`, ...).

**Version 1** (this document's first freeze, 2026-09-06, after
`run/contract.md` landed). Any breaking change to a signature or field
below gets a new version marker in this same section, loudly, before Lane
C's build can break silently.

**Version 2 addendum (2026-09-07, P8 — `run/iteration-queue.md`):** additive
only, nothing below removed or renamed. A fourth `ExtractionKind`,
`ExtractionRepoPointer = "repo-pointer"`, plus five new `RegisterEntry`/
`RegisterInput` fields (`RemoteURL`, `LocalPath`, `LocalCheckoutStatus`,
`RepoDescription`, `RoutingSurface`) and one new type (`LocalCheckoutState`,
values `"present"`/`"absent"`) — see the "P8: repo pointer records" section
below. `identityFor`'s input (`Locator` alone) is UNCHANGED — this was the
Director's own named trap; the 24 records live before this addendum kept
their exact ids after it (see that section's own evidence). Also: source
views now live at `stagingDir/"05 - Sources"/`, not `"01 - Sources"` (the
code already did this before this addendum; the line below was stale doc
drift, corrected here).

## Package `github.com/jonhill90/agent-estate/estate/internal/catalogue`

### Existing surface (unchanged by this run)

`Build()`, `Source`, `Catalogue`, `HealthState`, `BuildCodexSource`,
`BuildClaudeSource`, `BuildSeedPDFSource`, `SeedPDFDescriptors`,
`DefaultCodexRoot`, `DefaultClaudeRoot` — the health-report snapshot API
this package already had. Nothing here changed shape; `cmd/sourcecatalogue`
with no subcommand still calls exactly this, unchanged.

### New: the persistent register

```go
type ExtractionKind string
const (
    ExtractionPDF          ExtractionKind = "pdf"
    ExtractionConversation ExtractionKind = "conversation"
    ExtractionRepoDocs     ExtractionKind = "repo-docs"
)

type EntryStatus string
const (
    StatusActive      EntryStatus = "active"
    StatusNeedsReview EntryStatus = "needs_review"
)

// The three closed contract.md §1 axes this package validates against.
// Owned by 05 - System/tags.md (kind, lifecycle) and contract.md itself
// (access); this package only enforces them, it does not extend them.
var ContractKinds map[string]bool         // "kind/repo".."kind/tool"
var ContractReviewStates map[string]bool  // "lifecycle/candidate".."lifecycle/rejected"
var ContractAccessLevels map[string]bool  // "public", "private", "scoped"

const DefaultReviewState = "lifecycle/candidate"
const DefaultAccess = "private"

type RegisterEntry struct {
    ID                   string          // contract `id`
    Locator              string          // contract `locator`
    Kind                 string          // contract `kind` (validate against ContractKinds before passing in)
    ExtractionKind       ExtractionKind  // NOT a contract field -- this package's own extraction mechanism
    Provenance           string          // contract `provenance`
    Attribution          string          // contract `attribution` ("unknown" is valid and explicit)
    Authority            string          // contract `authority`
    Scope                string          // NOT a contract field -- kept from this package's own pre-contract field list
    Access               string          // contract `access` (validate against ContractAccessLevels)
    AccessDetail         string          // free-text elaboration of Access, optional
    Owner                string          // NOT a contract field -- accountable operator, distinct from Attribution
    Freshness            string          // contract `freshness`
    ReviewState          string          // contract `review-state` (validate against ContractReviewStates)
    WhyIndexed           string          // NOT a contract field
    DerivativeLinks      []string        // contract `derivative-links`
    ObservedRevision     string          // contract `revision`/`hash`
    Status               EntryStatus     // NOT a contract field -- this package's own drift signal
    ExtractionStatus     string          // never blank: "extracted: ...", "not applicable: ...", or "could not extract: ..."
    ExtractionCachePath  string          // private cache path, empty if nothing was extracted
    RegisteredAt         time.Time       // contract `observed-at`, first value
    LastRefreshedAt      time.Time       // contract `observed-at`, latest value
}

type Register struct { Entries []RegisterEntry }

type RegisterInput struct {
    Kind, Locator, Provenance, Attribution, Authority, Scope,
        Access, AccessDetail, Owner, Freshness, ReviewState, WhyIndexed string
    ExtractionKind  ExtractionKind
    DerivativeLinks []string
}

func DefaultRegisterDir() (string, error)             // ~/.local/state/agent-estate/catalogue, or $ESTATE_CATALOGUE_REGISTER
func LoadRegister(dir string) (*Register, error)      // missing file -> empty Register, not an error
func SaveRegister(dir string, reg *Register) error    // atomic (temp file + rename)

func (reg *Register) Register(registerDir string, in RegisterInput, now time.Time) (entry RegisterEntry, created bool)
func (reg *Register) Refresh(registerDir, id string, now time.Time) (RegisterEntry, bool)
func (reg *Register) Acknowledge(id string, now time.Time) (RegisterEntry, bool)
func (reg *Register) Show(id string) (RegisterEntry, bool)
func (reg *Register) List() []RegisterEntry
```

**Identity.** `Register`'s id is a function of `Locator` alone (SHA-256,
first 8 bytes, `"src-"` prefix) — re-registering the same locator under a
different `Kind` or `ExtractionKind` updates the existing entry in place,
never creates a second one. Two different locators always get two
different ids.

**Idempotence.** Calling `Register` twice with the same `RegisterInput`
produces `created=false` the second time and leaves `len(Entries)`
unchanged — see `internal/catalogue/register_test.go`'s
`TestRegister_Idempotent`.

**Drift.** `Refresh` re-observes the source's current revision marker. A
changed marker flips `Status` to `StatusNeedsReview` regardless of what it
was before, and never touches `Authority`, `Scope`, `Access`, `Owner`,
`WhyIndexed`, or `DerivativeLinks` — those are the operator's own declared
facts, not something a drift check may overwrite. Only `Acknowledge` moves
`StatusNeedsReview` back to `StatusActive`, and only when explicitly
called — never automatically.

**Validation is the caller's job, not this package's.** `RegisterEntry`'s
struct itself does not reject an invalid `Kind`/`Access`/`ReviewState` —
`cmd/sourcecatalogue`'s `register` subcommand validates against
`ContractKinds`/`ContractAccessLevels`/`ContractReviewStates` before
calling `Register`. Lane C's own callers (if any call `Register` directly
rather than shelling out to the CLI) must do the same validation, or use
the CLI as the enforcement point.

### New: source views (staging only, never the live vault)

```go
func GenerateSourceView(e RegisterEntry, now time.Time) string
func WriteViewsStaging(entries []RegisterEntry, stagingDir string, now time.Time) (int, error)
```

Writes OKF 0.2 Markdown, one file per entry, under
`stagingDir/"05 - Sources"/<id>.md`. Every call regenerates every current
entry's file from scratch (atomic per-file, temp+rename) — safe to call
repeatedly, self-healing after an interrupted prior run. This run's own
staging output lives at `run/views-staging/05 - Sources/` (sibling to
this file, outside any lane's git worktree) — never inside a lane's repo,
never the live Obsidian vault. (A stale `01 - Sources/` subdirectory from
before the INMAPS relayout may still be present there — historical, not
current output; the reconciliation of that leftover is separate,
deferred work, not this addendum's.)

### P8: repo pointer records (`run/iteration-queue.md`)

```go
type ExtractionKind string
const ExtractionRepoPointer ExtractionKind = "repo-pointer"

type LocalCheckoutState string
const (
    LocalCheckoutPresent LocalCheckoutState = "present"
    LocalCheckoutAbsent  LocalCheckoutState = "absent"
)

// New RegisterEntry / RegisterInput fields (both), meaningful only when
// ExtractionKind == ExtractionRepoPointer, empty/omitted otherwise:
RemoteURL           string              // the repo's canonical GitHub URL
LocalPath           string              // a local checkout path -- LAST KNOWN path even when absent, never cleared
LocalCheckoutStatus LocalCheckoutState  // RegisterEntry only -- typed absence, re-observed by Refresh
RepoDescription     string              // one line, what the repo is
RoutingSurface      string              // where the repo's own routing lives, e.g. "AGENTS.md"
```

**The identity trap, and how this addendum avoids it.** `identityFor`
(unexported, register.go) still hashes `Locator` alone — unchanged
signature, unchanged input. For a repo-pointer entry, `Locator` is set to
the same value as `RemoteURL` (a stable, machine-independent identifier —
`LocalPath` varies per machine and is deliberately never used for
identity). `RemoteURL`/`LocalPath` are carried as separate fields
alongside `Locator`, never merged into it and never derived from each
other, per this section's own binding rule (`iteration-queue.md`'s P8
note). Evidence this held: the 24 records live in `05 - Sources` before
this addendum landed have the exact same 24 ids after it — see the PR
body's before/after listing.

**Typed absence, not an error.** `LocalCheckoutStatus` is `LocalCheckoutAbsent`
when `LocalPath` is empty, does not exist, or is unreachable on the
machine that last registered/refreshed the entry — never an error return,
never a blank string standing in for "no path." `LocalPath` itself is
NOT cleared when this happens; it stays the last-known location (see
`LocalCheckoutAbsent`'s own doc comment in `register.go`).

**Extraction.** A repo-pointer never reads file content. Its only
observation is `git rev-parse HEAD` against `LocalPath`, read-only, when
present — the same shell-out-to-a-real-CLI convention `internal/corpus`
already uses for `sqlite3`. `ExtractionCachePath` is always empty for
this kind.

**Management.** Repo-pointer records are managed only through
`cmd/sourcecatalogue register`/`refresh` (`-extraction-kind repo-pointer
-remote-url ... -local-path ... -repo-description ... -routing-surface
...`) — never a hand-edited pointer file, same as every other kind.

## Package `github.com/jonhill90/agent-estate/estate/internal/knowledge`

Lane C does not need this section to build against Lane B — it documents
how the catalogue integrates into `estate knowledge`, for completeness.

`Config` gained one field: `CataloguePath string` (defaults, via
`DefaultConfig()`, to `catalogue.DefaultRegisterDir()`). `Generate` runs a
sixth source, `catalogue-source`, alongside the original five — reading
`CataloguePath`'s register (never writing it) and producing one `Item` per
`RegisterEntry`. `catalogue-source` classifies private by default (see
`classify.go`), the same "unclassified means private" rule every other
source-level default follows. A `CataloguePath` that is empty, missing, or
holds zero entries reports as one honestly-non-fatal `SourceResult` — most
machines running `estate knowledge` have registered nothing.

This never regenerates the shared `estate knowledge` index — it only adds
a source `Generate` reads from when a caller (this run, or later, Lane
C's CLI wiring) points `ESTATE_KNOWLEDGE_INDEX` at an explicit, non-shared
path per the existing `resolveOutputPath` convention
(`internal/knowledge/write.go`).

## What Lane C should NOT do

- Reach into `cacheDir`, `observe`, `identityFor`, `driftStatus`, or any
  other unexported name in `internal/catalogue` — none of it is stable
  across a future refactor of extraction internals.
- Regenerate `run/views-staging/` by hand — always through
  `WriteViewsStaging` (or the `sourcecatalogue register`/`refresh -views-dir`
  CLI flags, which call it).
- Write to the live vault from anything Lane B built — this package has no
  vault-write path at all, by design.
