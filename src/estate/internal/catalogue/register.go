// Register turns the health-report catalogue above into a persistent
// registration service (agent-estate#1139 K5, lane-b of the 2026-09-06
// knowledge-architecture run). A Source (catalogue.go) is a snapshot
// this package recomputes every time it is asked; a RegisterEntry is a
// durable record that survives between runs, at
// ~/.local/state/agent-estate/catalogue/register.json -- never in the
// vault, never Git-tracked (agent-estate-lanes/run/brief-lane-b.md
// deliverable 1).
//
// RegisterEntry's fields realign onto agent-estate-lanes/run/contract.md
// (Lane A's source-record contract, landed after this package's own
// fields were first scaffolded): ID/Locator/Kind/Provenance/Attribution/
// Authority/Access/ObservedRevision/Freshness/ReviewState/
// DerivativeLinks are the contract's own field names or their direct Go
// equivalent. Two fields are this package's own, not the contract's: Ex-
// tractionKind (which of this run's three proven extraction mechanisms
// -- pdf/conversation/repo-docs -- applies; the contract's Kind is a
// content-type tag from tags.md's closed axis, a different question) and
// Status (StatusActive/StatusNeedsReview, this package's own drift
// signal -- distinct from the contract's ReviewState, which is an
// editorial "has a human accepted this" state; see EntryStatus's own
// doc comment for the difference).
//
// Identity is derived from Locator alone (identityFor below) -- the same
// real-world source re-registered under a different declared Kind or
// ExtractionKind is still the same source -- never from the wall clock
// or an incrementing counter, so re-registering the same source twice is
// a no-op on the record's identity: same id, entry updated in place,
// zero duplicates (deliverable 4). Refresh re-observes a source's
// revision marker and flips it to StatusNeedsReview the moment that
// marker changes -- it never rewrites Authority, Scope, or any other
// hand-declared field to make the record LOOK current again; a changed
// source is a finding for a human to review, not something this package
// silently absorbs (deliverable 5).
package catalogue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// EntryStatus is a registered source's own drift-review state -- distinct
// from HealthState (catalogue.go), which is about whether a source could
// be read at all, AND distinct from ReviewState (contract.md's editorial
// lifecycle axis: has a human accepted this record's content as current
// truth). A source can be perfectly HealthPopulated, ReviewState
// "lifecycle/current", and still be StatusNeedsReview because its
// content changed since it was last reviewed.
type EntryStatus string

const (
	// StatusActive means this entry's ObservedRevision matches what was
	// last reviewed -- nothing has drifted since.
	StatusActive EntryStatus = "active"
	// StatusNeedsReview means Refresh observed a different revision than
	// the one on record. The entry's descriptive fields (Authority,
	// Scope, ...) are left exactly as they were; only a human
	// re-registering or explicitly acknowledging this state moves it
	// back to StatusActive.
	StatusNeedsReview EntryStatus = "needs_review"
)

// ContractKinds is contract.md §1's closed `kind` axis (05 - System/
// tags.md in the vault) -- the value a RegisterEntry's Kind field must
// be one of. Extending it needs a reviewed change to tags.md itself, per
// that file's own extension rule; this package validates against it but
// does not own it.
var ContractKinds = map[string]bool{
	"kind/repo":       true,
	"kind/doc":        true,
	"kind/transcript": true,
	"kind/decision":   true,
	"kind/skill":      true,
	"kind/tool":       true,
}

// ContractReviewStates is contract.md §1's closed `review-state` axis
// (tags.md's `lifecycle` axis). Every RegisterEntry defaults here to
// "lifecycle/candidate" -- nothing this package registers has been
// accepted by a human as current truth simply by being catalogued.
var ContractReviewStates = map[string]bool{
	"lifecycle/candidate":  true,
	"lifecycle/current":    true,
	"lifecycle/superseded": true,
	"lifecycle/rejected":   true,
}

// ContractAccessLevels is contract.md §1's closed `access` axis.
var ContractAccessLevels = map[string]bool{
	"public":  true,
	"private": true,
	"scoped":  true,
}

// DefaultReviewState is what a newly registered entry gets when the
// caller does not specify one -- see ContractReviewStates' own comment.
const DefaultReviewState = "lifecycle/candidate"

// DefaultAccess is the fail-safe default when a caller does not specify
// -access -- the same "unclassified means private" reasoning
// internal/knowledge's classify.go already applies to its own sources.
const DefaultAccess = "private"

// ExtractionKind names one of this run's three proven extraction
// mechanisms -- what observe() (extract.go) dispatches on. Distinct from
// Kind, which is the contract's content-type tag; a "pdf" ExtractionKind
// might carry Kind "kind/doc", and a "conversation" ExtractionKind
// carries Kind "kind/transcript".
type ExtractionKind string

const (
	ExtractionPDF          ExtractionKind = "pdf"
	ExtractionConversation ExtractionKind = "conversation"
	ExtractionRepoDocs     ExtractionKind = "repo-docs"
)

// RegisterEntry is one durable registration: everything this package
// keeps about one source between runs. Field-level comments name which
// contract.md §1 field each one realizes; see this file's own doc
// comment for the two fields (ExtractionKind, Status) that are this
// package's own.
type RegisterEntry struct {
	RemoteURL       string     `json:"remote_url,omitempty"`
	LocalPath       string     `json:"local_path,omitempty"`
	LocalState      LocalState `json:"local_state,omitempty"`
	RoutingSurfaces []string   `json:"routing_surfaces,omitempty"`
	ViewID          string     `json:"view_id,omitempty"` // Stable INMAPS view identity; ID retains existing catalogue references.
	// ID realizes contract `id`. Derived from Locator (identityFor) --
	// stable across repeated registrations of the same source, never
	// reassigned, never reused after a record is deleted (contract §1's
	// own "no resurrected old id" rule -- this package has no delete
	// path today, so that rule is trivially honored, not yet exercised).
	ID string `json:"id"`
	// Locator realizes contract `locator`.
	Locator string `json:"locator"`
	// Kind realizes contract `kind` -- one of ContractKinds. Validated
	// by cmd/sourcecatalogue at registration time, not by this struct
	// itself, so a test can still construct an entry with an
	// intentionally invalid Kind to exercise that validation.
	Kind string `json:"kind"`
	// ExtractionKind is this package's own field -- see doc comment
	// above. Not part of contract.md.
	ExtractionKind ExtractionKind `json:"extraction_kind"`

	// Provenance realizes contract `provenance` -- who or what produced
	// THIS RECORD (never the original).
	Provenance string `json:"provenance"`
	// Attribution realizes contract `attribution` -- who or what
	// authored the original this record points at. "unknown" is a valid,
	// explicit value (contract §1: "unknown means not offered, never
	// broken"), never left blank.
	Attribution string `json:"attribution"`
	// Authority realizes contract `authority`.
	Authority string `json:"authority"`
	// Scope is this package's own addition (agent-estate#1139 K2's
	// original field list, predating the contract) -- not a contract.md
	// §1 field, kept because it answers a question authority/scope
	// answer separately elsewhere in this repo's own catalogue.go.
	Scope string `json:"scope"`
	// Access realizes contract `access` -- one of ContractAccessLevels.
	Access string `json:"access"`
	// AccessDetail is free text elaborating Access -- e.g. a handling
	// constraint beyond the three-value enum. Optional.
	AccessDetail string `json:"access_detail,omitempty"`
	// Owner is this package's own addition, predating the contract:
	// who is accountable for this source's registration being accurate
	// -- distinct from Attribution (who authored the ORIGINAL).
	Owner string `json:"owner"`
	// Freshness realizes contract `freshness`.
	Freshness string `json:"freshness"`
	// ReviewState realizes contract `review-state` -- one of
	// ContractReviewStates. Defaults to DefaultReviewState.
	ReviewState string `json:"review_state"`
	// WhyIndexed is this package's own addition, predating the
	// contract -- never left blank; see brief-lane-b.md's field list.
	WhyIndexed string `json:"why_indexed"`
	// DerivativeLinks realizes contract `derivative-links`.
	DerivativeLinks []string `json:"derivative_links,omitempty"`

	// ObservedRevision realizes contract `revision`/`hash` -- a SHA-256
	// for a single file, a manifest hash for a directory tree, or a unit
	// count for a live append-only log. Never a timestamp: a marker that
	// changes on every observation regardless of content would make
	// drift detection meaningless.
	ObservedRevision string `json:"observed_revision"`
	// Status is this package's own drift-review state -- see EntryStatus.
	Status EntryStatus `json:"status"`

	// ExtractionStatus states plainly what extraction, if any, this
	// entry's ExtractionKind supports and what happened: "extracted:
	// <detail>", "not applicable: <reason>", or "could not extract:
	// <reason>". Never blank -- an entry with no extraction path says so
	// instead of leaving the field empty for a reader to misread as
	// "forgotten."
	ExtractionStatus string `json:"extraction_status"`
	// ExtractionCachePath is where extracted content, if any, was
	// written -- always under the same private register directory,
	// never Git-tracked, never containing raw operator prompts.
	ExtractionCachePath string `json:"extraction_cache_path,omitempty"`

	// RegisteredAt is this entry's first `observed-at` (contract §1).
	RegisteredAt time.Time `json:"registered_at"`
	// LastRefreshedAt is this entry's latest `observed-at` (contract
	// §1) -- updated by both Register (re-registration) and Refresh.
	LastRefreshedAt time.Time `json:"last_refreshed_at"`
}

// Register is the whole persisted set of entries -- the file's own root
// object, so future fields (a schema version, say) can be added without
// changing every entry.
type Register struct {
	Entries []RegisterEntry `json:"entries"`
}

// registerFileName is the one file this package ever writes inside its
// register directory.
const registerFileName = "register.json"

// DefaultRegisterDir resolves the private register's directory the same
// way internal/corpus.Path() and internal/knowledge's DefaultConfig
// resolve theirs: an explicit env var override first, a fixed default
// second. Never under the vault, never inside this repository.
func DefaultRegisterDir() (string, error) {
	if p := os.Getenv("ESTATE_CATALOGUE_REGISTER"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "agent-estate", "catalogue"), nil
}

// registerFilePath is dir's own register.json.
func registerFilePath(dir string) string {
	return filepath.Join(dir, registerFileName)
}

// cacheDir is where dir's extraction caches live -- one subdirectory per
// entry id, so two entries' extractions can never collide.
func cacheDir(dir, id string) string {
	return filepath.Join(dir, "cache", id)
}

// LoadRegister reads dir's register.json. A directory that has never had
// anything registered into it is not an error -- it returns an empty
// Register, the same "not yet populated" reading Build() gives an absent
// source root, never a failure a caller has to special-case.
func LoadRegister(dir string) (*Register, error) {
	data, err := os.ReadFile(registerFilePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return &Register{}, nil
	}
	if err != nil {
		return nil, err
	}
	var reg Register
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, err
	}
	assignViewIDs(reg.Entries)
	return &reg, nil
}

// SaveRegister writes reg to dir/register.json via a temp-file-plus-
// rename so a process killed mid-write never leaves a half-written
// register.json behind for the next LoadRegister to choke on --
// deliverable 4's "interrupted view generation repairs on rerun"
// depends on the register itself surviving an interruption intact.
func SaveRegister(dir string, reg *Register) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	assignViewIDs(reg.Entries)
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	tmp := registerFilePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, registerFilePath(dir))
}

// identityFor is the stable identity this whole package keys on:
// Locator alone, never anything time-based and never Kind -- the same
// real-world source re-registered under a different declared Kind is
// still the same source. Two RegisterInputs with the same Locator always
// resolve to the same id, which is exactly what makes Register
// idempotent.
func identityFor(locator string) string {
	sum := sha256.Sum256([]byte(locator))
	return "src-" + hex.EncodeToString(sum[:8])
}

// RegisterInput is everything a caller supplies to register or
// re-register one source. Fields not named here (ID, ObservedRevision,
// Status, ExtractionStatus, the two timestamps) are this package's own
// to compute -- a caller never invents them.
type RegisterInput struct {
	RemoteURL       string
	LocalPath       string
	RoutingSurfaces []string
	Kind            string
	ExtractionKind  ExtractionKind
	Locator         string
	Provenance      string
	Attribution     string
	Authority       string
	Scope           string
	Access          string
	AccessDetail    string
	Owner           string
	Freshness       string
	ReviewState     string
	WhyIndexed      string
	DerivativeLinks []string
}

// applyDefaults fills the two contract fields (Access, ReviewState) that
// have a fail-safe default when a caller leaves them empty -- never
// silently leaving them blank, which contract.md §1 marks required.
func (in RegisterInput) applyDefaults() RegisterInput {
	if in.Access == "" {
		in.Access = DefaultAccess
	}
	if in.ReviewState == "" {
		in.ReviewState = DefaultReviewState
	}
	if in.Attribution == "" {
		in.Attribution = "unknown"
	}
	return in
}

// Register upserts one entry into reg by identity, observes its current
// revision and extraction outcome via observe (extract.go), and reports
// whether this call created a new entry (created=false means an existing
// entry with the same Locator was updated in place -- zero duplicate
// identities, per deliverable 4). registerDir is where extraction caches
// for this entry are written; it does not have to equal the directory
// reg itself was loaded from, but every call in this package's own CLI
// passes the same value for both.
func (reg *Register) Register(registerDir string, rawIn RegisterInput, now time.Time) (entry RegisterEntry, created bool) {
	assignViewIDs(reg.Entries)
	in := rawIn.applyDefaults()
	id := identityFor(in.Locator)
	revision, extractionStatus, cachePath := observe(in.ExtractionKind, observationLocator(in.Locator, in.LocalPath), cacheDir(registerDir, id))

	for i := range reg.Entries {
		if reg.Entries[i].ID != id {
			continue
		}
		e := &reg.Entries[i]
		e.RemoteURL = in.RemoteURL
		e.LocalPath = in.LocalPath
		e.RoutingSurfaces = append([]string(nil), in.RoutingSurfaces...)
		e.LocalState = localState(in.LocalPath)
		e.Kind = in.Kind
		e.ExtractionKind = in.ExtractionKind
		e.Provenance = in.Provenance
		e.Attribution = in.Attribution
		e.Authority = in.Authority
		e.Scope = in.Scope
		e.Access = in.Access
		e.AccessDetail = in.AccessDetail
		e.Owner = in.Owner
		e.Freshness = in.Freshness
		e.ReviewState = in.ReviewState
		e.WhyIndexed = in.WhyIndexed
		e.DerivativeLinks = in.DerivativeLinks
		e.ExtractionStatus = extractionStatus
		e.ExtractionCachePath = cachePath
		e.LastRefreshedAt = now
		// Re-registering does not clear a StatusNeedsReview flip on its
		// own -- see driftStatus's own doc comment: only an unchanged
		// revision against what NeedsReview last observed, or an
		// explicit Acknowledge, clears it.
		e.LocalState = localState(e.LocalPath)
		e.Status = driftStatus(e.Status, e.ObservedRevision, revision)
		e.ObservedRevision = revision
		return *e, false
	}

	e := RegisterEntry{
		RemoteURL: in.RemoteURL, LocalPath: in.LocalPath, LocalState: localState(in.LocalPath), RoutingSurfaces: append([]string(nil), in.RoutingSurfaces...),
		ID:                  id,
		Kind:                in.Kind,
		ExtractionKind:      in.ExtractionKind,
		Locator:             in.Locator,
		Provenance:          in.Provenance,
		Attribution:         in.Attribution,
		Authority:           in.Authority,
		Scope:               in.Scope,
		Access:              in.Access,
		AccessDetail:        in.AccessDetail,
		Owner:               in.Owner,
		Freshness:           in.Freshness,
		ReviewState:         in.ReviewState,
		WhyIndexed:          in.WhyIndexed,
		DerivativeLinks:     in.DerivativeLinks,
		ObservedRevision:    revision,
		Status:              StatusActive,
		ExtractionStatus:    extractionStatus,
		ExtractionCachePath: cachePath,
		RegisteredAt:        now,
		LastRefreshedAt:     now,
	}
	reg.Entries = append(reg.Entries, e)
	assignViewIDs(reg.Entries)
	return reg.Entries[len(reg.Entries)-1], true
}

// driftStatus is the one place this package decides whether a
// newly-observed revision keeps an entry StatusActive or flips it to
// StatusNeedsReview. previous is the revision already on record before
// this observation; next is what was just observed. A changed revision
// always sets StatusNeedsReview, regardless of the entry's current
// status -- content that changed again while still under review is
// still a finding, not something a second silent overwrite should hide.
// An unchanged revision leaves the current status exactly as it was:
// this function never heals a NeedsReview entry back to Active on its
// own -- see Acknowledge for the only path that does.
func driftStatus(current EntryStatus, previous, next string) EntryStatus {
	if previous != "" && previous != next {
		return StatusNeedsReview
	}
	if previous == "" {
		return StatusActive
	}
	return current
}

// Refresh re-observes id's current revision and extraction outcome, and
// reports the resulting entry. It never touches Authority, Scope,
// Access, Owner, WhyIndexed, or DerivativeLinks -- those are the
// operator's own declared facts about the source, not something a
// revision check may overwrite.
func (reg *Register) Refresh(registerDir, id string, now time.Time) (RegisterEntry, bool) {
	for i := range reg.Entries {
		if reg.Entries[i].ID != id {
			continue
		}
		e := &reg.Entries[i]
		revision, extractionStatus, cachePath := observe(e.ExtractionKind, observationLocator(e.Locator, e.LocalPath), cacheDir(registerDir, e.ID))
		e.LocalState = localState(e.LocalPath)
		e.Status = driftStatus(e.Status, e.ObservedRevision, revision)
		e.ObservedRevision = revision
		e.ExtractionStatus = extractionStatus
		e.ExtractionCachePath = cachePath
		e.LastRefreshedAt = now
		return *e, true
	}
	return RegisterEntry{}, false
}

// Acknowledge is the only path that moves an entry from
// StatusNeedsReview back to StatusActive without a revision change --
// an explicit operator decision that the drift already found has been
// reviewed, never an automatic side effect of Register or Refresh.
func (reg *Register) Acknowledge(id string, now time.Time) (RegisterEntry, bool) {
	for i := range reg.Entries {
		if reg.Entries[i].ID != id {
			continue
		}
		reg.Entries[i].Status = StatusActive
		reg.Entries[i].LastRefreshedAt = now
		return reg.Entries[i], true
	}
	return RegisterEntry{}, false
}

// Show returns the one entry named by id.
func (reg *Register) Show(id string) (RegisterEntry, bool) {
	for _, e := range reg.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return RegisterEntry{}, false
}

// List returns every entry, in registration order.
func (reg *Register) List() []RegisterEntry {
	out := make([]RegisterEntry, len(reg.Entries))
	copy(out, reg.Entries)
	return out
}

// Preserve hash identities and citations. Allocate dated view addresses once,
// in register insertion order, and persist them with the operational register.
func assignViewIDs(entries []RegisterEntry) {
	used := map[string]bool{}
	for _, e := range entries {
		if e.ViewID != "" {
			used[e.ViewID] = true
		}
	}
	for i := range entries {
		if entries[i].ViewID != "" {
			continue
		}
		day := entries[i].RegisteredAt.UTC().Format("2006-01-02")
		for n := 1; ; n++ {
			id := fmt.Sprintf("SRC-%s-%03d", day, n)
			if !used[id] {
				entries[i].ViewID = id
				used[id] = true
				break
			}
		}
	}
}

// LocalState records checkout availability independently of the declared remote.
type LocalState string

const (
	LocalUnspecified LocalState = "unspecified"
	LocalMissing     LocalState = "missing"
	LocalPresent     LocalState = "present"
	LocalUnavailable LocalState = "unavailable"
)

func localState(path string) LocalState {
	if path == "" {
		return LocalUnspecified
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return LocalMissing
	}
	if err != nil || !info.IsDir() {
		return LocalUnavailable
	}
	return LocalPresent
}
func observationLocator(locator, local string) string {
	if local != "" {
		return local
	}
	return locator
}
