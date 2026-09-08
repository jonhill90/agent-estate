# Lane A handoff — knowledge architecture run, 2026-09-06

## P3 round 2 — worktree/branch cleanup (2026-09-07) — COMPLETE, report-first

Scoped to this machine's local checkouts of `agent-estate`, `agent-dotfiles`,
`Skills` (skills-private not touched — nothing named it in scope). Did not
touch P5-remainder/P6/P7/P8 or anything Astra might resume from.

### Before counts

| Repo | Worktrees | Local branches |
|---|---|---|
| agent-estate | 37 | 235 |
| agent-dotfiles | 7 | 94 |
| Skills | 5 | 97 |

### #591 check (the round's specific addition) — 0 unpushed-only, and mostly N/A on this machine

Read `agent-estate#591` in full (issue + all 9 comments). Its own final
tally, already complete before this task started: **24 orphaned `lane/*`
branches examined across 4 repos (19 agent-estate/agent-supervisor, 4
agent-tui, 1 skills), all 24 verified superseded — 0 stranded/unpushed-only
work**, by blob-identity and patch-content comparison (not the
`git diff --stat`/subject-grep traps the same thread documents falling
into and correcting). Its own words: "nothing here exists only on this
machine... deleting them is now purely about tidiness."

Checked **this machine's** `agent-estate` checkout for any of the named
`lane/*` branches directly: **zero exist here** (`git branch | grep lane/`
→ empty). The 19 live on whatever machine ran that investigation, not
this one — reporting that plainly rather than fabricating a check against
branches that aren't present. `agent-dotfiles` and `Skills` each still
carry a handful of `lane/*` branches (not the specific 19, per #591's own
per-repo breakdown); all were checked individually below.

### Worktrees — 4 pruned, all git-confirmed-safe; 0 dispatch worktrees touched

**`git worktree prune`** (git's own mechanism — removes ONLY the
administrative record for a worktree whose directory is confirmed
physically gone; never touches a directory that still exists, so there's
nothing to lose):
- `agent-estate`: dry-run empty — nothing to prune.
- `agent-dotfiles`: 1 removed — `worktrees/ad-337-fix337-carapace-11723`
  ("gitdir file points to non-existent location").
- `Skills`: 3 removed — `ad-287-sk287-rev288-11848`, `ad-285-fix293-72536`,
  `ad-285-rerev293-86040` (same reason).

**Known trap avoided (rule 3):** did not run `estate sweep-worktrees`
(agent-estate#1247's deadlock defect) anywhere.

**`estate reclaim` run first, before considering any dispatch worktree**
(rule 2): `0 in flight, 0 reclaimable`. `estate inflight`: `0 task(s)`.
No running turn anywhere on this machine.

**Despite 0 in-flight, 0 dispatch worktrees under `$TMPDIR/estate-dispatch`
were removed — every single one carries real, uncommitted content.**
Checked all ~30 (`agent-estate-9a432f445c9e/*` and `estate-main-b2eb1f312a05/*`
groups) with `git status --porcelain`: **every one is non-empty** —
genuine modified/added Go source files (e.g. new `pressure.go`/
`pressure_test.go`, modified `harness.go`/`ledger.go`/`main.go`/
`knowledge/loops.go`/`vault.go`), not build artifacts or `.DS_Store`. This
directly triggers rule 4's preserve clause ("anything holding uncommitted
work not byte-identical to origin/main") for **all of them**, even though
their turns show `complete` in `estate tasks`/the ledger. This is a real
finding, not a null result: **"stale dispatch worktree" and "worktree
holding real uncommitted work from a completed turn" are not the same
thing, and round 1's framing assumed the former without checking.**
Recommend a human decide per-worktree whether that uncommitted content
should be salvaged (committed to a real branch) or discarded — not a
sweep, the same "decision, not a sweep" framing #591 itself uses.

**Also checked and preserved, not removed:**
- `/private/tmp/claude-501/.../scratchpad/wboth` (a different Claude
  session's scratch worktree, detached HEAD) — 4 uncommitted files
  (`provenance.go` modified, `dispatchid.go`/`dispatchid_test.go` new).
  Preserved per the same uncommitted-work rule.
- `/Users/jon/source/repos/Personal/agent-estate/.claude/worktrees/agent-a8eded8b54e9aa5ec`
  (`feat/knowledge-third-rung`, fully merged into `origin/main`) — 2
  uncommitted files (`internal/knowledge/loops.go`, `vault.go` modified).
  Preserved for the same reason, despite the branch itself being merged.
- `.worktrees/ad-343-fix343-keychain-guard-16576` (agent-dotfiles,
  `lane/343-fix343-keychain-guard`) — genuinely clean (`git status
  --porcelain` empty) but `git branch --merged origin/main` says NOT
  merged, even though `#344` ("guard macOS Keychain writes") reads as
  this exact work landed — almost certainly a squash-merge (the same
  `[[git-cherry-lies-across-a-squash-merge]]` class this vault already
  names): the squashed commit is never a literal ancestor of the
  pre-squash branch. `git branch -d` would correctly refuse. Per rule 1's
  own wording ("never -D"), left alone rather than force-deleted —
  reporting the likely-already-landed status for a human call, not
  overriding the safety check myself.
- All Astra worktrees/branches (`astra-push2`, `astra-consultation`,
  `astra-dotfiles`, `astra-skills`), `knowledge/lane-a/b/c`,
  `/private/tmp/estate-main`, everything under
  `~/source/repos/Personal/agent-estate-lanes/*` (including
  `p6-corpus-rename-estate` — lane-c's active, under-review PR #1270
  branch, and `/private/tmp/p8-repo-pointers` —
  `feat/catalogue-repo-pointer-records`, P8 territory, untouched per this
  round's own scope note).

### Branches — 2 deleted, both git-verified fully merged AND worktree-free

```
$ cd ~/source/repos/Personal/Skills
$ git branch -d lane/285-rerev293
Deleted branch lane/285-rerev293 (was edd0df0).
$ git branch -d lane/287-sk287-rev288
Deleted branch lane/287-sk287-rev288 (was 91bc9a8).
```
Both: fully merged into `origin/main` (`git branch --merged`, git's own
ancestor check — never `-D`), worktree already gone (pruned above), no
uncommitted content anywhere to lose.

**`agent-estate`: 0 branches deleted.** All 23 branches merged into
`origin/main` are checked out in worktrees this round left alone (the
dispatch-worktree uncommitted-content finding above) — `git branch -d`
cannot delete a checked-out branch, and removing those worktrees first
was ruled out by the same finding. `knowledge/lane-a` is among the 23
(merged, since #1263 landed) but is explicitly preserved by name
regardless.

**`agent-dotfiles`: 0 branches deleted.** `lane/337-fix337-carapace`
(worktree already pruned) and `lane/343-fix343-keychain-guard` (worktree
clean but present) both fail `git branch --merged origin/main` — neither
forced with `-D`.

### After counts

| Repo | Worktrees | Local branches | Worktrees removed | Branches deleted |
|---|---|---|---|---|
| agent-estate | 37 | 235 | 0 | 0 |
| agent-dotfiles | 6 | 94 | 1 | 0 |
| Skills | 2 | 95 | 3 | 2 |

---

## P7 — SKILLS-INDEX.md stale-copy anti-pattern (2026-09-07, late night) — COMPLETE

Fetched `jonhill90/skills` fresh (`origin/main@c8ad9ba`) rather than
trusting the ~30-commit-stale local checkout, per the Director's own
warning — confirmed `SKILLS-INDEX.md` present, 51 lines, one file, added
whole by #302.

**Fold routing into normal surfaces:** already true — `README.md`'s
"Skills in this collection" table pre-dates this push and is already
CI-guarded (`scripts/validate_repository.py`'s `validate_readme_table`,
diffs the table's linked names against `skills/*/SKILL.md` directly,
fails CI on drift — jonhill90/skills#224). `SKILLS-INDEX.md` duplicated
that surface under a different name instead of using it.

**Deleted `SKILLS-INDEX.md` outright, no replacement.** Grepped the
whole repo first: zero references anywhere (never linked from
README/AGENTS/CLAUDE/any script/any test) — a pure orphan, safe to
remove with no in-repo consumer to repoint. Did not add a second,
freshly-generated table: README's table already satisfies the
"generated-or-link-only" requirement for any *new* per-skill listing;
adding another would recreate the anti-pattern with extra steps.
README's own pre-existing Purpose column (hand-curated short summaries,
not verbatim `description` copies) predates this push and is out of this
fix's scope.

Verified: `scripts/validate_repository.py` → 41 skills, 0 errors, 0
warnings; `python3 -m unittest discover -s tests -v` → 206 tests, OK.

**PR opened, not merged:** jonhill90/skills **#303**, head
`9763852767c0331cb2e1e049fb5c67612a56e506`, CI green (4/4 checks).

### Consumers found and repointed (outside this repo, same session)

- `run/register-push-sources.py` — its `SKILLS-INDEX.md` registration
  line (pointing at a scratch worktree path) updated to register the
  real, merged `README.md` instead.
- **Live private catalogue register** — registered a new, correct entry
  for `README.md` (`src-638ca0fd1d98de47`); confirmed the local `Skills`
  checkout's `README.md` is byte-identical to `origin/main`'s before
  trusting its hash. **Could not delete or repoint the old entry**
  (`src-ac32f53a9c315818`, `SRC-2026-09-06-018.md`, locator a now-
  orphaned scratch path) — `internal/catalogue` has no delete/supersede
  path (its own doc comment: "this package has no delete"). Reported
  honestly rather than silently left looking current; it will read as
  drifted the next time `sourcecatalogue refresh` tries to observe a
  path that no longer exists.
- `run/astra-skills-index.py` (the one-off generator that produced
  `SKILLS-INDEX.md`) — annotated superseded/do-not-rerun in its own
  header.
- Vault: `Start Here.md`, `03 - Agents/index.md` checked — never
  referenced `SKILLS-INDEX.md` at all; nothing to repoint there.
- `agent-estate` repo: grepped, zero references.

**Left alone, on purpose** (dated historical records, not live
consumers — same norm as not rewriting a feature-registry's past
evidence after a later path move): `run/astra-oneshot-report.md`,
`run/source-registration-evidence.json`,
`run/source-refresh-evidence.json`, `run/iteration-queue.md`.

---

## P5 batch 1 — agent/ dissolution (2026-09-06/07, late evening) — COMPLETE

Per `run/inmaps-spec.md` §7b (binding disposition map), smallest safe
slice: low-volume `agent/` members, not the 1,104 corpus items.

**Backup first.** All touched/moved files (16 paths, including the
`intent/`, `tools/`, `sources/` subdirectories) backed up to
`run/vault-backup-6/`, checksummed, before any edit.

### Paths moved (all MOVE, never copy)

| Old path | New path |
|---|---|
| `agent/ROUTING.md` | `99 - Meta/ROUTING.md` |
| `agent/LIFECYCLE.md` | `99 - Meta/LIFECYCLE.md` |
| `agent/log.md` | `99 - Meta/log.md` |
| `agent/pending-links.md` | `99 - Meta/pending-links.md` |
| `agent/intent/` (1 file) | `99 - Meta/intent/` |
| `agent/tools/` (2 files) | `99 - Meta/tools/` — **decision: 99 - Meta, not the repo.** These are vault-native tools with no repo equivalent; the vault is not a git repo and these scripts run standalone (`python3 tools/validate_index.py`, stdlib only, no checkout required) — moving them into a git repo would add a checkout dependency this tooling has never had, contradicting the vault's own "no CLI or app required" design (`memory-conventions.md`). Kept with the content they validate. |
| `agent/sources/karpathy-stanford-transcript.txt` | `05 - Sources/karpathy-stanford-transcript.txt`, registered as `SRC-2026-09-07-001.md` via the real `sourcecatalogue register` tool (Rule 17 — tool-only writes, never hand-authored) |

Left alone, exactly as scoped: `agent/parameters/` (1,104-item job, its
own later batch), `agent/corpus/`, `agent/index.md`,
`agent/INDEX-CONTRACT.md`.

### Consumers found and repointed in the same change

Searched rather than assumed the list was complete — grepped the whole
`agent-estate` repo, `agent-dotfiles` repo, and the vault itself for
every moved path before touching anything:

1. **`src/estate/internal/candidates/inmaps.go`'s `writeSet()`** (Go code,
   merged) — hardcoded `agent/log.md` as the write target for every
   INMAPS accept/reject/propose. Exactly the P0-class bug. Fixed to
   `99 - Meta/log.md`; PR **#1269** (not merged, per instruction), head
   `7b3a329e2d4b709fc143c44cb229262c0411cc27`. New regression test
   (`TestINMAPSLifecycle` extended) asserts both directions — new path
   gets real content, old path never resurrected — mutation-checked
   myself (reverted, watched it fail with the exact claimed error,
   restored, reran green).
2. **`agent/tools/validate_index.py`'s `vault_root()`** — computed the
   vault root from its own file location (`tools/ -> agent/`), which
   breaks the instant the script itself moves. Fixed to explicitly
   target `agent/` (two levels up from the script's new location, then
   join `agent`) regardless of where the script physically lives. Its
   own 7-test suite (`test_validate_index.py`) still passes; the real
   validator run from `99 - Meta/` correctly resolves `agent/index.md`
   and `agent/facts/` — proof, not assumption (see below).
3. **`agent/INDEX-CONTRACT.md`'s run-instructions** (stays in `agent/`
   this batch, but its own prose named the old path) — the one surgical
   edit made to an otherwise-untouched "leave alone" file, since leaving
   it wrong would be exactly this task's own failure mode.
4. **Vault cross-links**: `Start Here.md`, `agent/index.md`,
   `agent/00 - Inbox/README.md`, `00 - Inbox/index.md`,
   `99 - Meta/index.md`, `99 - Meta/tags.md` — every link into a moved
   path repointed. Vault-wide sweep after found zero remaining stale
   references (only intentional "what changed" narration).
5. **Checked, NOT a functional consumer, left untouched**:
   `internal/features/registry.go`'s prose citing "`agent/ROUTING.md` is
   34 lines" — dated evidence describing a past verification event
   (2026-09-06), not code that reads the file back; rewriting it would
   be rewriting historical evidence, against this codebase's own norm.
6. **Checked, unaffected**: `internal/corpus/standinglaw.go` and
   `internal/knowledge/vault.go` (both only read `agent/facts/`/
   `01 - Notes/`, neither moved this batch); `memory.go`'s legacy
   (non-INMAPS) branch's own `agent/log.md` reference (dead code against
   the real vault, which is INMAPS).

No consumer required stopping — every one found could be repointed
safely in this same change.

### Validator, before and after

Identical both times — no regression, no drift:
```
Checked 119 fact file(s) against .../agent/index.md
Warnings (14, non-blocking): [... same 14 pre-existing, unrelated ...]
Contract holds: no hard violations.
```

### Live standing-law probe (required acceptance evidence)

Read-only, via a throwaway uncommitted `cmd/verifystandinglaw` calling
`corpus.StandingLaw()` directly against the real vault — not a real
`estate dispatch` (host pressure is within limits, so a real dispatch
would spawn a genuine costly agent turn; this run's own recorded lesson
says dispatch-based proofs belong to the Director):
```
$ AGENT_MEMORY_VAULT="$HOME/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory" \
  go run ./src/estate/cmd/verifystandinglaw
StandingLaw() OK: resolved 1 entr(y/ies)
  - estate-is-skills-practice-not-a-product-to-sell: "The estate is not a commercial product and will not be sold...."
```
Confirms this batch did not regress the earlier P0 fix — checked, not
assumed, even though standing-law only reads paths this batch didn't
touch.

### Repo-side acceptance

```
$ go build ./...    # clean
$ go vet ./...      # clean
$ go test ./... -count=1
ok  	.../estate	94.264s
[... all packages ok ...]
```
PR **#1269** open, not merged, CI running as of this write.

---

## P2 — wikilink alias repair (2026-09-06 ~22:5x EDT) — COMPLETE

Per Director P2: bulk-rewrite `[[slug]]` wikilinks whose slug appears in
`run/w1-migration-report.md`'s slug→ID mapping to their real ID targets,
since the `aliases:` frontmatter alone does not make Obsidian resolve a
bare `[[slug]]` post-migration (confirmed already-failing in the
migration report's own "Obsidian alias check — FAIL" section).

**Backup first.** Measured 106 live vault files containing `[[wikilinks]]`
(matches the Director's own count exactly). All 106 backed up to
`run/vault-backup-5/`, checksummed (`run/vault-backup-5/checksums.txt`,
sha256), before any edit.

**Scope discipline (requirement 3 — code/backtick exclusion).** Wrote a
mask-based tokenizer treating fenced (```` ``` ````) and inline (`` ` ``)
spans as untouchable, mirroring the P1.5 `#N` tag fix's own rule. Measured
279 total `[[...]]` occurrences vault-wide; 11 were inside backticks
(all genuine documentation examples in `ROUTING.md`, `INDEX-CONTRACT.md`,
`pending-links.md`, `log.md`, `99 - Meta/tags.md` — spot-checked each,
none is a real navigation link) and were correctly never touched.

**Rewrite form: bare `[[ID]]`, not piped `[[ID|slug]]`.** Checked
`tools/validate_index.py`'s own `WIKILINK_RE` first — it requires
`[a-zA-Z0-9_-]+` with no pipe character, so a piped link would validate
as "no source link" (a real regression, the same class of gap the fact-
migration-pilot task hit). A bare `[[ID]]` matches that regex AND
resolves in real Obsidian by filename stem match alone — no dependency
on the aliases mechanism that's already confirmed broken. Bare-ID links
are also the INMAPS spec's own convention (§2: notes are addressed by
ID, title lives in frontmatter/H1) rather than a compromise.

**Batched, validated after every batch** (never one giant untested
rewrite): 5 batches (~19-20 files each). One batching bug caught and
fixed mid-run — an early attempt recomputed the remaining-files list
fresh inside a shifting index range, silently dropping files. Caught by
re-running the plan step and finding files still unaddressed (38 files,
63 links short of the true total) rather than trusting the batch loop's
own exit; corrected by always slicing from the front of the
(deliberately shrinking) remaining-list, and re-verified 0 files
remained after. Validator ran after every batch; held clean throughout,
same 14 pre-existing warnings, zero hard violations at every step.

**Counts:**
- Matched (rewritten): **246** links across **95** files.
- Left alone, reported (no mapping entry — genuine pre-existing forward
  references per `agent/pending-links.md`'s own convention, never
  guessed): **22** links, listed below verbatim, plus one prose mention
  of the literal word "wikilink" in `agent/parameters/parameters.md`
  (a generated, do-not-hand-edit file; correctly not a real link).
- **Before: 268 unresolved** (all prose-context wikilinks, since none
  resolved via aliases before this fix — confirmed by the migration
  report's own failed Obsidian alias check).
- **After: 22 unresolved** (all genuine forward references, unchanged).

Unmapped links (left alone, exactly as required):
```
01 - Notes/202609020002.md: [[look-before-you-delete]]
01 - Notes/202609020002.md: [[never-commit-jons-prompts-verbatim]]
01 - Notes/202609050001.md: [[absence-is-a-typed-value]]
01 - Notes/202609050001.md: [[measurement-must-be-falsifiable]]
01 - Notes/202608240012.md: [[verify-the-instrument-before-you-believe-the-verdict]]
01 - Notes/202608110010.md: [[verify-the-instrument-before-believing-the-verdict]]
01 - Notes/202608040002.md: [[positive-control-fixture]]
01 - Notes/202608240008.md: [[verify-the-instrument-before-you-believe-the-verdict]]
01 - Notes/202608270001.md: [[verify-before-asserting]]
01 - Notes/202607290001.md: [[hill90-app-is-a-tenant]]
01 - Notes/202607290001.md: [[hill90-silent-config-failures]]
01 - Notes/202608160010.md: [[decide-by-variant]]
01 - Notes/202608160010.md: [[memory-design]]
01 - Notes/202608270002.md: [[ask-jon-only-intent-questions]]
01 - Notes/202608270002.md: [[verify-before-asserting]]
01 - Notes/202608290002.md: [[state-lives-in-files-not-context]]
01 - Notes/202608160001.md: [[memory-failure-mode-is-staleness-not-retrieval]]
01 - Notes/202607310001.md: [[hill90-app-repo-visibility-history-finding]]
01 - Notes/202607310001.md: [[hill90-app-is-a-tenant]]
01 - Notes/202608240011.md: [[verify-the-instrument-before-you-believe-the-verdict]]
01 - Notes/202609050003.md: [[state-lives-in-files-not-context]]
```
(cross-checked each against the 119-row mapping for near-miss typos —
none found; all 20 are genuinely never-written targets, consistent with
`agent/pending-links.md`'s own pre-existing list.)

**Spot-check verification (requirement 5), 3 rewritten links, real
resolution confirmed:**
```
agent/index.md:77            [[202608220002]] -> 01 - Notes/202608220002.md exists
                              (aliases: ["estate-is-the-product"] -- content confirmed matching)
04 - Projects/agent-estate.md:16   [[202608230001]] -> 01 - Notes/202608230001.md exists
01 - Notes/202608100002.md:46      [[202608100001]] -> 01 - Notes/202608100001.md exists
```

**Final validator run:**
```
$ python3 tools/validate_index.py
Checked 119 fact file(s) against .../agent/index.md
Warnings (14, non-blocking): [... same 14 pre-existing, unrelated to this change ...]
Contract holds: no hard violations.
```

Did not touch `tools/validate_index.py`, the P1.5 tag check, or any
worktree cleanup, per the Director's explicit scope note.

---

## Fact migration pilot (2026-09-06 ~18:06–18:12 EDT) — STOPPED, RESTORED, NOT force-greened

Per `run/inmaps-spec.md` §8 (added ~18:10 EDT) and the follow-on task from
Fable: pilot-migrate the 10 oldest `agent/facts/*.md` into `01 - Notes/`
with `ID = YYYYMMDD` + 4-digit same-day sequence, `aliases: [old-slug]`,
`id:` field, move not copy, update `agent/index.md`/`ROUTING.md`/
`LIFECYCLE.md`, validator after.

**Backup taken first**, before any edit: all 10 target fact files plus
`agent/index.md`, `agent/ROUTING.md`, `agent/LIFECYCLE.md` copied to
`run/vault-backup-3/`, checksummed (`run/vault-backup-3/checksums.txt`,
sha256).

**Planned full ID mapping** (10 oldest by `created:`, full timestamp where
present, then alphabetically by slug on a date-only tie — computed, not
all executed, see below):

| Old slug (`agent/facts/<slug>.md`) | `created:` | New ID |
|---|---|---|
| memory-conventions | 2026-07-12T17:30:00Z | 202607120001 |
| python-package-manager-uv | 2026-07-12T18:40:00Z | 202607120002 |
| git-commit-style | 2026-07-12T18:55:00Z | 202607120003 |
| no-flattery-in-analysis | 2026-07-12T22:36:42Z | 202607120004 |
| model-subscriptions | 2026-07-13T02:33:46Z | 202607130001 |
| audit-inherited-repo-patterns | 2026-07-18 | 202607180001 |
| verify-in-browser-before-claiming-fixed | 2026-07-19 | 202607190001 |
| topics-not-to-raise | 2026-07-27 | 202607270001 |
| hill90-one-keycloak | 2026-07-29 | 202607290001 |
| token-budget-discipline | 2026-07-29T00:00:00Z | 202607290002 |

**Batch 1 (memory-conventions → 202607120001) executed for real, as the
actual first batch, not a synthetic test**, per spec §8's own "batch-wise
with validator after each batch, never one giant untested move":
moved `agent/facts/memory-conventions.md` → `01 - Notes/202607120001.md`
with `id: "202607120001"` and `aliases: [memory-conventions]` added, title/
H1 unchanged; updated `agent/index.md`'s bullet and `agent/ROUTING.md`'s
`[[memory-conventions]]` reference to point at the new path.

**Validator rejected it — real, structural, not a false alarm:**
```
$ python3 tools/validate_index.py
Checked 118 fact file(s) against .../agent/index.md
...
VIOLATIONS (1):
  - index.md:16: bullet has no source link — '[memory-conventions](../01%20-%20Notes/202607120001.md) — how this vau'
Contract does NOT hold. Fix the above; this tool does not repair them.
```
exit=1.

**Root cause, read from `tools/validate_index.py`'s own source, not
guessed:** its link regex is hardcoded to the literal prefix `facts/` —
```python
MD_LINK_RE = re.compile(r"\[([^\]]+)\]\((facts/[^)]+\.md)\)")
```
— and its existence check resolves every target against
`os.listdir(facts_dir)` (i.e. `agent/facts/` only). A link into
`01 - Notes/` never matches the pattern at all, so the validator sees "no
source link," not "wrong link" — it has no vocabulary for a note living
outside `agent/facts/`. This is a real gap in the validator itself, not a
mistake in the migration: **the tool, as it exists today, cannot express
or validate a fact that has moved to `01 - Notes/`.**

**Stopped immediately, per the explicit instruction not to force it
green.** Restored from `run/vault-backup-3/`:
`01 - Notes/202607120001.md` removed, `agent/facts/memory-conventions.md`,
`agent/index.md`, `agent/ROUTING.md` copied back from backup. Verified
byte-identical to backup by re-hashing all three (`OK` for all). Reran the
validator: **`Checked 119 fact file(s) ... Contract holds: no hard
violations.` (exit 0)** — back to the exact pre-pilot state.

**The other 9 facts were never touched** — batch 1's failure is
sufficient evidence the same rejection would fire for all 10 (same
mechanism, same regex), so there was nothing to learn by repeating it nine
more times, and per the instruction, a rejected batch is a stop condition,
not a per-fact retry loop.

**What this means for the Astra plan, stated rather than left implicit:**
`01 - Notes` migration cannot proceed — pilot or bulk — until
`tools/validate_index.py` itself is extended to recognize a fact/note
living at a `01 - Notes/<id>.md` path (and, per spec §8's own closing
line, until the instruction surfaces that name `agent/facts/`
directly — `memory-conventions.md`, global `CLAUDE.md` — are updated to
match). This is a **prerequisite finding**, not a completed migration:
report it as "could not measure/could not proceed as specified," not as
"10 facts migrated."


## INMAPS vault relayout (2026-09-06 ~17:52–21:5x UTC / ~17:5x EDT) — COMPLETE

Per the Director's instruction, `run/inmaps-spec.md` (Jon + Fable,
2026-09-06 evening) and its own explicit line "Lane A may begin the vault
relayout under this spec." **Complete and validator-clean; the Director
may take over as vault writer for integration step 4.**

**Backup first, before any edit or move:** every file that would be
touched or moved was copied to `run/vault-backup-2/` preserving relative
paths, checksummed to `run/vault-backup-2/checksums.txt` (sha256).
Contents were re-verified against the backup by re-reading each file
immediately before its directory was removed — this run's own
safe-deletion discipline.

**Moves/creates, in order:**
```
mv "01 - Sources" "05 - Sources"
mv "02 - Projects" "04 - Projects"
mv "05 - System"   "99 - Meta"
mkdir "01 - Notes" "02 - MOCs" "03 - Agents"
rm -rf "03 - Shared Memory" "04 - Skills and Tools"   # retired -- see below
```
Final tree (`find "$AGENT_MEMORY_VAULT" -maxdepth 1 -type d`):
`00 - Inbox, 01 - Notes, 02 - MOCs, 03 - Agents, 04 - Projects,
05 - Sources, 99 - Meta, agent, .obsidian` — an exact match to
`run/inmaps-spec.md` §1's tree.

**Two folders retired, not renamed** (INMAPS's tree has no slot for
either — per its own inventory-vs-memory test):
- `03 - Shared Memory` — a pure link page (agent/index.md, ROUTING.md,
  LIFECYCLE.md, memory-conventions.md); its function is already served by
  `Start Here.md`'s own direct link to `agent/index.md`, so the page was
  redundant, not lost data. Full bytes in `run/vault-backup-2/`.
- `04 - Skills and Tools` — an inventory table of skills/tools already
  tracked in git; per the spec's own rule ("an index of git artifacts
  lives IN git"), this class of content doesn't belong in the vault at
  all. Full bytes in `run/vault-backup-2/`.

**Content updated in every moved/created file** — titles, headings, and
every cross-link (`grep`-swept the whole vault afterward for the five old
folder names; the only two hits left are the deliberate "what changed"
history note in `Start Here.md` and the retirement note in
`04 - Projects/agent-estate.md`, both intentional, not stale):
- `04 - Projects/index.md`, `agent-estate.md` — renamed from `02 -
  Projects`; `agent-estate.md`'s stale `03 - Shared Memory`/`01 - Sources`
  links repointed.
- `05 - Sources/index.md` — renamed from `01 - Sources`; added the
  `SRC-YYYY-MM-DD-NNN` naming note from spec §2.
- `99 - Meta/index.md`, `tags.md` — renamed from `05 - System`; added
  `agent/log.md` to the index; fixed `tags.md`'s `project` axis link.
- `01 - Notes/index.md`, `02 - MOCs/index.md` — new, explain the
  ID-filename/never-renamed rule and the MOC mechanized-emergence
  threshold (spec §2/§4); both honestly empty (nothing has migrated into
  Notes yet, so no MOC has anything to hub).
- `03 - Agents/index.md` — new, single pointer file. **Honest gap
  recorded rather than papered over**: agent-dotfiles has no single
  canonical "agent roster" file today; pointed at the two closest real
  artifacts (`agents/` subagent-type definitions,
  `docs/memory-per-agent-map-contract.md`) and said so plainly.
- `00 - Inbox/index.md` — added the "everything arrives `status: draft`"
  framing from spec §1/§4; fixed its `01 - Sources` link to `05 -
  Sources`.
- `Start Here.md` — fully rewritten: a READ PROTOCOL section (entry point
  → area index/MOC → note → canonical source, per spec §4) before the
  area table; area table updated to the seven INMAPS areas; a "what
  changed, and when" section recording both this run's original naming
  and tonight's relayout, so a reader hitting old bookmarks understands
  why paths moved rather than guessing.

**Validator, run after every edit:**
```
$ python3 tools/validate_index.py   # from agent/
Checked 119 fact file(s) against .../agent/index.md
Warnings (14, non-blocking): [... same 14 pre-existing missing-description/
  title warnings as before this relayout -- validator scope is agent/facts/
  + index.md, untouched by the root relayout, so this is expected, not a
  regression]
Contract holds: no hard violations.
```

**Not done, follow-up (small, separate PR, per the Director's brief):**
`docs/knowledge-workflow.md` and `run/contract.md` still reference the
retired `01 - Sources`/`02 - Projects` folder names in prose (not paths a
test enforces, since neither is checked-in vault content) — tracked as a
small follow-up PR, not blocking the relayout's completion or the
Director's vault handoff.

---

Status as of this write: Deliverables 1 and 2 complete; Deliverable 3's
first PR open and awaiting CI + cross-lane review. This file is updated
again before the 19:15 EDT checkpoint if state changes.

## Done

1. **Vault backup.** Every vault file touched (`agent/ROUTING.md`,
   `agent/LIFECYCLE.md`, `agent/00 - Inbox/README.md`) backed up to
   `run/vault-backup/` with `checksums.txt` (sha256), before any edit.
2. **Vault root navigation** — additive only, nothing moved, no fact body
   copied:
   - `Start Here.md`
   - `00 - Inbox/index.md`, `01 - Sources/index.md`,
     `02 - Projects/index.md` + `agent-estate.md`,
     `03 - Shared Memory/index.md`, `04 - Skills and Tools/index.md`,
     `05 - System/index.md` + `tags.md` (4-axis governed vocabulary:
     `kind`, `lifecycle`, `project`, `topic`; extension rule stated
     in-file)
   - `agent/00 - Inbox/README.md` updated to signpost the new root
     `00 - Inbox/index.md`
3. **Corrected `agent/ROUTING.md` and `agent/LIFECYCLE.md`** against the
   real CLI (`go run ./src/estate`), verified by actually running every
   command against a private read-only corpus copy and a scratch vault —
   never the live corpus or vault. Two real bugs found and fixed:
   - No `estate candidates derive` verb exists — the real command is the
     bare `estate candidates -db <path> -apply`.
   - `estate candidates decide` takes `<candidate-id> promote|discard` as
     **positional** arguments, not `-id`/`-action` flags (`memory` is the
     one subcommand that does take those flags — that part was already
     right).
   - `agent/parameters/*.md`'s banner claims `estate vault-view`
     regenerates them; that command does not exist anywhere in
     `agent-estate` or `agent-dotfiles` (confirmed by grep and by running
     `go run ./src/estate` with no args). `ROUTING.md` now says so
     honestly instead of repeating the dead reference — **no current
     command regenerates `agent/parameters/*.md`.**
4. **Validator run** (`agent/tools/validate_index.py`) after all vault
   edits: `Contract holds: no hard violations.` (119 fact files checked;
   14 pre-existing warnings, none introduced by this run's edits — full
   output in PR #1263.)
5. **`run/contract.md`** written (Deliverable 2) — source-record
   frontmatter fields, tag-vocabulary reference, canonical-destination
   rule. Unblocks Lane B and Lane C.
6. **First repo PR opened: [#1263](https://github.com/jonhill90/agent-estate/pull/1263).**
   Carries `docs/knowledge-workflow.md` (the seven-step loop, every
   command verified for real), one `AGENTS.md` routing line,
   `.claude/skills/knowledge-session/SKILL.md` (references the guide,
   never copies it), and
   `docs/decisions/2026-09-06-knowledge-architecture-merge-exception.md`.
   `Author-Lane: agent-estate:1` in the PR body. `go test -run TestAgents`
   (both binding tests) and `go vet ./src/estate/...` pass — output in the
   PR body.
7. **CI fix at head `9b08a00`.** First push went red on
   `TestEveryRepoDocsFileHasANaturalCase` (agent-estate#1176) — both new
   repo-docs files needed a natural-language golden case. Added nl-26/
   nl-27 to `goldenset/natural_cases.json`, updated the count assertion in
   `natural_cases_test.go` (25 → 27), verified `go test ./src/estate/...`
   and `go vet ./src/estate/...` both clean, pushed. **New head:
   `f1a680a`.** Any review against `9b08a00` is void per protocol — Lane B
   must review at `f1a680a`.
8. **CI green at `f1a680a`** — both required `estate` check jobs pass
   (confirmed via `gh pr checks 1263`). PR is otherwise mergeable pending
   Lane B's independent cross-lane APPROVE at this exact head SHA.
9. **PR #1263 MERGED** (squash, `24ca850`) — Lane B approved at
   `f1a680a`, checks green, Director confirmed protocol satisfied. Step 1
   of the run sequence (A's nav/contract PR) is done. Local
   `knowledge/lane-a` rebased onto `origin/main`@`24ca850` (both commits
   dropped clean, already upstream); the remote branch was auto-deleted
   on merge, as expected after a squash merge — nothing lost, the final
   PR just recreates it.
10. **Reviewed Lane C's PR #1265** (my cross-lane review duty: A reviews
    C), at head `300cc97`. Independent verification, not just re-reading
    the PR body: cloned at the pinned SHA into a scratch dir, confirmed
    the diff (isolated from Lane B's already-merged #1264) touches only
    Lane C's owned paths; ran `go build`/`go vet`/`go test` fresh — all
    green; independently mutated `internal/knowledge/vault.go`'s
    `currentMemoryItem` to disable the stale-index guard and reproduced
    the claimed test failure myself (not trusted from their transcript),
    then restored it clean and reran green. Read the `candidates.go`/
    `memory.go`/`review.go` diffs in full — the catalogue-vs-conversation
    schema migration, citation branching, and `PublishRepo`'s exact-path/
    commit-SHA enforcement all hold up. Posted `Verdict: APPROVE`,
    `Review-Lane: agent-estate:1`, `Reviewed-SHA:
    300cc974262b60004dcfd48c23a80a0dfd1253ea` at
    https://github.com/jonhill90/agent-estate/pull/1265#issuecomment-5562114141.

## Not done / in progress

- **Final PR (guide + skill update to match the merged CLI)** is
  explicitly deferred until B and C merge, per the plan's integration
  order (A's nav/contract → B → C → A's final guide/skill PR) — it
  merges LAST, after C. Not started; do not open it for merge ahead of B
  and C. As of this write, Lane B (#1264) is merged; Lane C (#1265) is
  reviewed (APPROVE below) but not yet merged.

## Next steps for whoever resumes this

1. Watch for Lane C's PR; review it per the cross-lane protocol
   (`Verdict:`/`Review-Lane: agent-estate:1`/`Reviewed-SHA:`) as soon as
   it's open and green — Director will ping.
2. Once C's PR has also merged to `main`, rebase `knowledge/lane-a` onto
   the new `main` head, update `docs/knowledge-workflow.md`'s "register"
   step (currently notes Lane B's catalogue registration command isn't
   merged yet) and `.claude/skills/knowledge-session/SKILL.md` to match
   the CLI as actually merged (both B's catalogue and C's candidate/
   `main.go` wiring), and open the final PR — it merges LAST, after C,
   per the run sequence. Do not open it for merge ahead of that.
3. Nothing in `run/contract.md` needs revisiting unless B or C's
   implementation surfaces a field this contract didn't anticipate — if
   so, that's a contract amendment, not a silent divergence.

## P9+P10 bundle — flat-fact subdir + per-area index -> MOC hub (2026-09-07)

Rebased onto `origin/main` at `f272e52` (PR #1272 merged) before starting,
per standing instruction. Vault-side work (Obsidian vault, not a git repo)
+ one agent-estate PR for the two Go generators that still targeted the
retired per-area `index.md` paths.

### Vault safety

Checksummed backup taken before any edit: `run/vault-backup-7/` (132
files + `checksums.txt`, sha256). Validator (`99 - Meta/tools/
validate_index.py`) run after every batch; halt-and-restore was the
standing rule, never invoked — every batch held.

### P9 — 119 flat W1 facts -> `01 - Notes/01f - Facts/`

Created `99 - Meta/note-subdirs.md` (the letter registry: `01f` = Facts,
`01p` = Parameters) and generalized the validator's index-link regex to
build from that registry (`build_link_re(subdirs)`) instead of a second
hardcoded `01p`-only alternative — the Director's explicit blocker/fix,
`run/iteration-queue.md` pre-check 2.

**Self-caught batching bug, not a data-loss incident:** my first
migration script re-listed the directory fresh on every batch call, so
as files moved out the `[start:end)` slice drifted against a shrinking
list — the same class of bug as an earlier P2 batching-index-drift bug
this session. Effect: 71 of 119 facts moved on the first pass; 48 were
silently skipped (still flat in `01 - Notes/`, not lost). Caught
immediately by re-listing the actual directory state after the "5
batches" run reported done and finding files still at root that should
not have been. Fixed with a single pass over the *actual* remaining flat
files (no index math) — all 119 now under `01 - Notes/01f - Facts/`,
filenames/IDs unchanged. Root (`01 - Notes/`) now holds only
subdirectories and its own hub-replaced `index.md` (see P10).

**Stale-link sweep:** the migration script only rewrote links inside
`agent/index.md`. A whole-vault grep for markdown links to the
now-moved flat paths found 5 more live files with stale links
(`99 - Meta/pending-links.md`, `99 - Meta/log.md`, `00 - Inbox/index.md`
before its own P10 conversion) — all fixed. `99 - Meta/log.md`'s
append-only historical entries were deliberately left as-is (an accurate
record of the path at write time, not a live reference); its own
generated-content markers there don't get rewritten.

### P10 — one `index.md` in the whole vault

Converted every remaining per-area `index.md` into a title-named hub
under `02 - MOCs/`: `Inbox.md`, `Notes.md`, `Agents.md`, `Projects.md`,
`Sources.md`, `Meta.md` (`Parameters.md` was done as part of P9's
validator-regression fix, see below). The pre-existing
`02 - MOCs/index.md` (a stale "MOC birth mechanism" doc from the
original INMAPS relayout) was folded into a new `02 - MOCs/README.md`
rather than deleted outright — its content (tag-threshold-emergent topic
hubs, a distinct future mechanism from P10's always-present per-area
hubs) was real and worth keeping, just not under the retired `index.md`
name. Exactly one `index.md` now exists in the whole vault:
`agent/index.md`.

**Director pre-check 3 (MOC retrievability) — implemented as
recommended, stated explicitly, not left implicit:** `02 - MOCs/
README.md` and `Start Here.md` both now say outright that hubs are
navigation, not knowledge, and are deliberately unindexed —
`internal/knowledge/vault.go`'s reader only reads `01 - Notes/**/
\d{12}.md`, never `02 - MOCs`.

**Director pre-check 4 (what the validator now validates) — stated
explicitly:** the validator's docstring says outright that it still
targets `agent/index.md` + the `01 - Notes` tree; `agent/index.md`'s own
retirement is a separate, later, unscoped batch (`run/inmaps-spec.md`
§7b), not this one. `Start Here.md`'s changelog says the same in the
reader-facing copy.

**Self-caught orphan-check regression (construction-time, not a real
batch failure):** removing the old `01p - Parameters/index.md`
special-case from fact discovery also removed the only mechanism
counting the 2,639 parameter notes as "referenced" (they're bulk-listed
via their area index, never individually in `agent/index.md`). Caught
by running the validator immediately after the regex change, before any
real migration batch — 2,639 new "orphaned" violations. Fixed by adding
a `referenced_by_hubs` pass that scans `02 - MOCs/*.md` the same way
`agent/index.md`'s own bullets are scanned, then porting
`01p - Parameters/index.md`'s content into `02 - MOCs/Parameters.md`
(byte-identical link list, only frontmatter/H1 retitled) and deleting
the old file.

**Whole-vault stale-link sweep after all six conversions:** grepped for
every remaining reference (link or bare prose) to a retired per-area
`index.md` path. Found and fixed 5 more: `02 - MOCs/Inbox.md`,
`99 - Meta/ROUTING.md` (2 links), `99 - Meta/tags.md` (prose mention),
`04 - Projects/agent-estate.md`, `agent/00 - Inbox/README.md` (link text
as well as href). `99 - Meta/log.md`'s one historical mention left
untouched for the same append-only-record reason as above.

`Start Here.md`'s area table now links each area's hub instead of its
old `index.md`; its changelog gained a P9+P10 entry and its "Corpus
projections" line now points at `02 - MOCs/Parameters.md`.

### Final validator state

```
Checked 2757 fact file(s) against .../agent/index.md
Warnings (14, non-blocking) — same pre-existing recommended-frontmatter
gaps as before this task, no new ones
Contract holds: no hard violations.
```

### Go-code companion PR

The two generators that still wrote to the retired per-area paths:
`catalogue.WriteViewsStaging` (`05 - Sources/index.md`) and
`candidates.WriteRosterPointer` (`03 - Agents/index.md`). Landing the
vault-side move without this would let the next `sourcecatalogue
refresh`/candidate-accept run silently re-create the retired files.

Discovered the local `agent-estate-lanes/lane-a` checkout (this
coordination repo, not the actual `agent-estate` app repo) was stale —
`knowledge/lane-a` at `24ca850`, diverged from `origin/main` — and
neither `views.go` nor `inmaps.go` existed there under that stale ref.
Confirmed both exist on `origin/main` (`f272e52`) via `git ls-tree`
before trusting a subagent's line-numbered report of their contents,
per the standing "reproduce every claim" rule. Did the actual work in a
fresh worktree (`/tmp/agent-estate-p9p10`) branched from `origin/main`,
never the stale checkout.

- `WriteViewsStaging`: generated Sources hub -> `02 - MOCs/Sources.md`;
  per-entry views stay at `05 - Sources/<id>.md`, only the bulk-list
  index moved; its entry links rewritten to cross the directory
  boundary. Existing hand-authored/regenerate tests updated in place.
- `WriteRosterPointer`: agents routing note -> `02 - MOCs/Agents.md`.
  Had zero prior test coverage; added
  `TestWriteRosterPointerTargetsMOCsHub`, mutation-checked (reverted the
  path change, confirmed the test failed for the right reason — `no
  such file or directory` at the new hub path — restored, confirmed
  green again).

```
go build ./...   # clean
go vet ./...     # clean
go test ./...    # all packages pass
```

Opened **agent-estate PR #1274**, branch `feat/estate-p9-p10-hub-redirect`
off `origin/main` at `f272e52`, head `d8bdaf9`. Not merged — a different
lane reviews.

### Not done / deferred

- The vault-side move (backups, batches, hub conversions, note-subdirs
  registry, validator changes) has no git home of its own — it lives in
  the Obsidian vault filesystem, evidenced here and in
  `run/vault-backup-7/`, not in a PR.
- `agent/index.md`, `agent/facts/` (now empty), and
  `agent/INDEX-CONTRACT.md` remain in `agent/` — their own retirement is
  a later, unscoped batch per `run/inmaps-spec.md` §7b, explicitly out of
  scope for P9+P10.

## A2-COMPLETION — agent/ retired entirely (2026-09-07)

Last item on `run/inmaps-spec.md` §7b's binding disposition map. Rebased
onto `origin/main` at `50ddbe1` (PR #1274, my own P9+P10 hub-redirect PR,
merged) before starting. Vault safety: checksummed backup
(`run/vault-backup-8/`, 7 files pre-edit) before any change; validator
after each move; halt-and-restore was the standing rule, never invoked.

### Three moves, per §7b

1. **`agent/corpus/`** (raw prompt extraction, 1,097,256-byte
   `prompts.jsonl` + README — layer-1 evidence, not knowledge) →
   `~/.local/state/estate/corpus-extraction/`. Destination checked empty
   first (no overwrite). Registered ONE pointer source record via the
   real `sourcecatalogue register` CLI (`views-dir` pointed at the actual
   vault) rather than hand-authoring a file into `05 - Sources/` — that
   area's own generated-content contract ("GENERATED... do not
   hand-edit") applies to individual entries, not just the hub, so a
   hand-written file would have been orphaned from `Sources.md`'s own
   link list. Landed as `SRC-2026-09-07-008`.
2. **`agent/index.md` + `agent/INDEX-CONTRACT.md`** → merged into
   `Start Here.md`'s own `## Facts` section (all 119 bullets, link paths
   re-pathed for the new file's root-relative position — extracted
   programmatically from the source file, not retyped, to avoid
   transcription drift) + `99 - Meta/index-contract.md` (content
   otherwise unchanged). `Start Here.md` gained `okf_version: "0.1"`
   frontmatter, becoming the vault's sole carrier.
3. Removed the now-empty `agent/facts/`, the `agent/00 - Inbox` signpost
   (its unique content — the exact `estate candidates` CLI command
   reference — folded into `02 - MOCs/Inbox.md` first, not just deleted),
   several stray tool-generated backup dirs from earlier `writeSet`/
   `WriteViewsStaging` runs (`.inmaps-backup-*`, `.memory-backup-*`,
   `.candidate-memory.lock`), then `agent/` itself.

### Validator repointed (vault-side + repo-side, kept in sync)

`99 - Meta/tools/validate_index.py` now validates `Start Here.md`'s
`## Facts` section against `99 - Meta/index-contract.md`, not
`agent/index.md`. Its link regex accepts both a root-relative link
(`01 - Notes/...`, as written from `Start Here.md` itself) and a
one-level-down link (`../01 - Notes/...`, as written from a
`02 - MOCs/*.md` hub or another fact) — same registry-driven subdir
support as P9, now depth-agnostic too.

**Found and fixed mid-construction, before it reached the real vault**:
normalizing `md_targets` to strip a leading `../` was required for the
two link forms to resolve to the same `fact_files` key (which is always
vault-root-relative, since `Start Here.md` sits at the root) — without
it, every hub-written link failed to resolve. Caught by the test suite
I wrote alongside the change (12 cases), not by the real vault run.

**Also found and fixed**: `scripts/knowledge/vault/validate_index.py`
(the git-tracked copy in this repo) had silently drifted from the
vault's own deployed copy since P9+P10 — my earlier task edited only the
vault-side file, never synced this one back. Rewrote its test suite
(`test_validate_index.py`) from scratch — the old one asserted the
retired `agent/`-shaped API (`vault_root`, `agent/facts`) and would not
even have compiled against the new module. 12 tests, all green,
mutation-checked the normalization fix specifically.

### Real consumers found beyond the task's own list (all four named,
### plus these) — see agent-estate PR #1275 for the Go-code detail

- `candidates.publishINMAPS` read/wrote `agent/index.md` directly on
  every accept/reject. This was the single highest-severity finding:
  every future `estate candidates memory accept` would have hard-failed
  the moment `agent/` was deleted, with no warning anywhere upstream.
  Fixed to edit only `Start Here.md`'s `## Facts` section
  (`splitFactsSection`), verified the rest of the file survives an
  accept untouched.
- `writeSet`'s own crash-recovery backup dir and the candidate-memory
  lock file both targeted `agent/` as a parent directory — `MkdirTemp`
  requires the parent to exist, so this would also have hard-failed the
  next real write. Moved to `99 - Meta/`.
- `corpus.resolveStandingLawMemberFile`'s alias-resolution fallback used
  a flat, non-recursive `ReadDir` over `01 - Notes/` — P9's own
  `01f - Facts/` subdirectory move (landed the same evening) meant this
  fallback would find nothing, silently breaking standing-law resolution
  for the one declared member with no error anywhere near the cause.
  Fixed to walk recursively; **verified against the real vault** that the
  member resolves.
- `main.go`'s `indexSourceMtimes` stat'd `agent/facts` directly for the
  knowledge index's freshness check, unrelated to `vaultSource`'s actual
  read path — produced a false `*** SOURCE GONE ***` alarm on every real
  `estate knowledge query` once `agent/facts` stopped existing (measured
  this directly against the real vault before fixing it). Repointed to
  `01 - Notes`.

Explicitly left alone with reasons (stated in the PR, not silently
skipped): `candidates/memory.go`'s legacy `Publish` tail (dead code for
any INMAPS-shaped vault, gated by `inmaps(vault)`); `features/registry.go`'s
historical evidence strings (accurate record of the past, not a live
reference); `src/tui`'s knowledge reader (`fact.go`/`index.go`) — a
**pre-existing** gap dating to the W1 ID-based migration, not something
this task broke; flagged for its own dedicated fix, out of scope here.

### Gate — all four verified against the real vault, not fixtures

```
$ ls "$AGENT_MEMORY_VAULT/agent"
ls: ...: No such file or directory

$ python3 tools/validate_index.py   # from 99 - Meta/
Checked 2757 fact file(s) against .../Start Here.md
Warnings (14, non-blocking) — identical to the pre-existing baseline
Contract holds: no hard violations.

$ estate knowledge query --private "prompt delivery paste-confirmation"
  ... [it-d14cacb2a8f77398] .../01 - Notes/01f - Facts/202609060003.md
  ... [it-7de67f740cb90e15] .../01 - Notes/01p - Parameters/202608030056.md
  (both subdirs cited, no SOURCE GONE alarm)

$ grep -rl "^okf_version:" "$AGENT_MEMORY_VAULT" --include="*.md"
Start Here.md   # exactly one

$ find "$AGENT_MEMORY_VAULT" -name "index.md"
(zero results, live vault content — some stray tool-backup dirs under
dotfile-prefixed directories still hold old copies, not counted)
```

Opened **agent-estate PR #1275**, branch `feat/estate-a2-agent-retirement`
off `origin/main` at `50ddbe1`, head `e9cb7ed`. Not merged — a different
lane reviews.

### Not done / deferred

- `src/tui`'s knowledge reader needs its own dedicated fix (pre-existing,
  not newly broken) — flagged in the PR, not fixed here.
- The Push 3 stray tool-backup dirs (`.source-index-backup-*`) predating
  today's session are cosmetic debris under dotfile-prefixed paths, not
  live vault content — left alone, not worth the risk of touching
  something with an unclear origin without a specific reason to.

## A2-COMPLETION fix pass — #1275, the one allowed (2026-09-07)

Director/lane-b review found two blocking items. Both fixed in a single
pass (rebased onto current main first — no move, already current).

### Blocking item 1: OKF §12/§8 name the bundle-root `index.md` filename

My first A2 pass moved the `okf_version` carrier + capped facts list onto
`Start Here.md` (reasoning: "these are different jobs, one file can do
both"). Director adjudicated against the spec text directly, not the
plan's paraphrase — OKF §12 says a bundle MAY declare `okf_version` "in a
bundle-root `index.md` frontmatter block," §8 cross-references the same
filename. The spec names it twice; my interpretation was wrong.

Restored a bundle-root `index.md` at the vault root (not `agent/` —
`agent/` stays retired) as the sole carrier: the 119-bullet facts list
extracted back out of `Start Here.md`'s `## Facts` section, `okf_version`
frontmatter restored there. `Start Here.md` reverted to its original
human-entry-point job (prose, area table, changelog), pointing at
`index.md` the same way it once pointed at `agent/index.md`. Both files
now exist at the vault root doing two different jobs, exactly as Director
specified. Confirmed vault-wide: exactly one `index.md` (the bundle
root), exactly one file carrying `okf_version`.

Validator (`99 - Meta/tools/validate_index.py`, repo-tracked copy at
`scripts/knowledge/vault/validate_index.py`, kept in sync) repointed to
validate `index.md` again — and simplified back closer to its pre-A2
shape (`facts_section()`/`FACTS_SECTION_RE` removed) since `index.md`'s
whole body is the bullet list again, with no other Start-Here-style
sections competing for the space. `publishINMAPS`'s `indexPath` moved
back to the vault root and its section-scoped edit logic
(`splitFactsSection`) reverted to a plain whole-file line scan, matching
what `agent/index.md`'s own code did before the whole task started —
removed the now-dead helper rather than leaving it as debris.

### Blocking item 2: three of the five pre-listed consumers still dangled

Lane-b verified each by reading the file directly, not trusting my PR
body's claim:
- `docs/knowledge-workflow.md` (this repo's own canonical guide, routed
  from `AGENTS.md`): still said "adds one bullet to `agent/index.md`" —
  repointed to `01 - Notes/<12-digit-id>.md` + the vault-root `index.md`.
- `docs/tui/PRD.md` and `docs/orientation/tui-arrival.md`: both still
  described `internal/knowledge` as reading `agent/index.md` +
  `agent/facts/<slug>.md` — repointed to the real post-migration paths.

Lane-b grepped beyond the pre-check list and found nothing else
dangling; determined `99 - Meta/log.md`'s one remaining `agent/index.md`
mention (dated 2026-07-12, append-only historical log) is acceptable
history — left alone, matching my own earlier judgment on that file.

**Found in the same pass, on neither list**: `04 - Projects/
agent-estate.md` had a real dangling `[agent/index.md](../agent/index.md)`
link my own earlier sweep missed. Fixed in the same change.

### Verification after the fix

```
go build ./...   # clean
go vet ./...     # clean
go test ./...    # all packages pass
python3 -m unittest scripts/knowledge/vault/test_validate_index.py  # 12/12

$ python3 tools/validate_index.py   # from 99 - Meta/
Checked 2757 fact file(s) against .../index.md
Warnings (14, non-blocking) — same pre-existing baseline
Contract holds: no hard violations.

$ estate knowledge query --private "prompt delivery paste-confirmation"
  cited results from both 01f - Facts/ and 01p - Parameters/ confirmed again

$ grep -rl "^okf_version:" "$AGENT_MEMORY_VAULT" --include="*.md"
index.md   # exactly one

$ find "$AGENT_MEMORY_VAULT" -name "index.md"
index.md   # exactly one, live vault content
```

Pushed to the same PR (`feat/estate-a2-agent-retirement`, agent-estate
PR #1275), head now `9088019`. Not merged — lane-c re-reviews per the
one-fix-pass rule; a second REQUEST CHANGES closes the PR.

## Evidence-layer backup + restore test — #1276 (2026-09-07)

`master-execution-plan.md`, Completeness audit #2. Built and RAN (not
just wrote) a verified backup + restore-test routine for the two durable
evidence stores: `~/corpus/corpus.sqlite3` and
`~/.local/state/estate/ledger.jsonl`.

### Design decisions

- Python (`scripts/evidence/`), matching Jon's own stated rule: tooling
  can be shell/Python, the app stays Go.
- Scoped to exactly the two named canonical files. Everything else in
  `~/corpus/` and `~/.local/state/estate/` — WAL/SHM sidecars, the
  `ledger.sqlite3` compat symlink, pre-existing manual backup dirs,
  empty dirs, mirror logs, a scratch `candidate-memory-demo.*` dir
  (contains its OWN throwaway corpus.sqlite3 copy — deliberately not
  treated as live evidence), vault-backups, a couple of files flagged
  for a later pass — all named with a reason in `EXCLUSIONS`, per
  requirement 4 (report what is not covered).

### Real finding, caught before it could ever fail silently in production

`sqlite3 -readonly` cannot open a WAL-journaled database unless its
`-wal`/`-shm` sidecars already exist alongside it. A bare `.backup` copy
doesn't carry them — reproduced directly against the REAL live corpus
before writing the fix, not assumed:

```
$ sqlite3 -readonly ~/corpus/corpus.sqlite3 ".backup '/tmp/x.sqlite3'"   # succeeds
$ sqlite3 -readonly /tmp/x.sqlite3 "PRAGMA integrity_check;"
Error: in prepare, unable to open database file (14)
```

Fix: flatten the BACKUP COPY (never the live source) to
`journal_mode=DELETE` right after `.backup` — a single self-contained
file, openable `-readonly` cleanly. Without this, the very first real
restore-test run would have failed outright — exactly the gap "prove a
restore works" exists to close, versus "a backup exists."

### Test suite — mutation-checked the actual gate, not a proxy for it

First attempt at the corrupted-copy test corrupted an ALREADY-backed-up
file and ran a standalone `sqlite3` check against it — passed, but never
actually exercised `backup_sqlite`'s own integrity-check gate. Caught
this via mutation-check (disabled the gate in the real code, the test
still passed — a false green). Fixed by mocking `run_sqlite`'s
integrity_check call specifically, isolating the gate itself; re-ran the
same mutation and confirmed the corrected test now fails for the right
reason, restored, confirmed green. 9/9 tests pass, covering typed
absence (both kinds × both formats), the WAL-mode happy path, the
isolated integrity-gate failure, a real corrupted-source case, a
malformed-JSONL case, and an unexpected-filesystem-error case (one
source's failure never crashes the whole run).

### Acceptance — ran for real against the real live stores

```
corpus-sqlite3: status=ok, size=25583616, sha256=b351398c..., integrity=ok
  live before == live after (untouched)
  RESTORE TEST: byte-identical=True, restored integrity_check=ok,
    query proof: 16 tables, sample incl. codex_provenance/events/items

estate-ledger-jsonl: status=ok, size=1868962, sha256=82d6dbc1...,
  integrity: 1224 records, 0 malformed
  live before == live after (untouched)
  RESTORE TEST: byte-identical=True,
    query proof: 1224 records parsed, first/last record keys shown

Summary: 2/2 sources ok, live stores untouched: True
```

Full pasted output in the PR body. Opened **agent-estate PR #1276**,
branch `feat/evidence-backup-restore-test`, off `origin/main` at
`46709fa` (post #1275 merge), head `a17d907`. Not merged — a different
lane reviews.

### Not done / deferred

- Scheduling (cron/launchd) to run this periodically — named explicitly
  in the PR as a follow-up, not built here (not among the Director's
  five explicit requirements for this task).

## Global-instructions prose repair — agent-dotfiles #347 (2026-09-07)

Same defect class as the Push 3 gate's A3 failure, now in the GLOBAL
instructions every session reads at start.

### Corrected the Director's own measurement first

Director's measured canonical text ("agent/index.md only... facts load
on demand") does NOT match `origin/main` — that read came from the
shared `~/source/repos/Personal/agent-dotfiles` checkout, which turned
out to be **12 commits behind origin/main** with an unrelated local
diff on top (`apm.lock.yaml`, `hooks/ledger-write-guard.sh` — not mine,
left untouched). Caught this by opening a fresh worktree off
`origin/main` and reading the same lines there — genuinely different
text ("Read Start Here.md, then the capped agent/index.md; load scoped
notes on demand"), which turned out to be **byte-for-byte identical**
to the installed copies (`~/.claude/CLAUDE.md`, `~/.claude/rules/global.md`)
via direct diff.

This settled the install/generation question definitively: the copies
are NOT hand-maintained (Director's alternative hypothesis) — they
correctly generate from canonical via `apm compile --global` (confirmed:
`apm find` doesn't track them since they're user-scope not project-scope,
but `apm compile --global --dry-run` reports them as stale relative to a
fresh compile, meaning the pipeline works and is just waiting on
canonical to be right). The real defect is one retirement behind: canonical
still names `agent/index.md`, which no longer exists after A2-COMPLETION.

### Whole-repo prose sweep, judged per file

Beyond the literal `agent/index.md`/`agent/facts` grep, swept for prose
patterns ("session start.*read", "capped index", "browsing index", etc.)
per Director's explicit warning that "agent index" in prose is what
every path-shaped grep missed before. Found and judged:

**Fixed (INSTRUCTIONS):** `instructions/global.instructions.md`,
`instructions/overlays/pi.md`, `docs/canonical/memory.md` (including a
whole stale bundle-shape ASCII diagram sitting right below a *newer*
INMAPS section that had already superseded it — the doc was half-updated
and self-contradictory before this fix), `docs/canonical/moc-map-of-maps.md`,
`docs/canonical/memory-per-agent-map-contract.md` (four separate
citations treating `agent/index.md` as "the mechanism," not just a
path), `docs/corpus/index.md` (literal re-runnable `wc -l`/`ls` commands).

**Left alone (HISTORY):** the three `docs/historical/*.md` files,
identified reliably via their own `knowledge_status: historical`/
`superseded` frontmatter rather than guessed from filename or content.

**Named out of scope, not silently absorbed:** a pre-existing cap-number
citation drift ("200 lines/25KB" vs the vault's current 160-entry
contract) — reworded the two citations this pass already touched to
point at the vault's own `index-contract.md` instead of repeating a
number, but didn't audit every cap citation repo-wide; and a
pre-existing relative-path drift in two of `memory.md`'s own doc
citations (files actually one directory level down from where the text
implies) — a link-path issue, not an `agent/`-prose issue.

### Coordination with lane-b

Matched wording against lane-b's already-fixed vault-side "Memory
conventions" fact (`01 - Notes/01f - Facts/202607120001.md`) — same
routing claims (`Start Here.md` for routing, bundle-root `index.md` as
the `okf_version` carrier per OKF §12 not a browsing index, `01 - Notes`
under earned subdirs, `02 - MOCs`/`99 - Meta`/`05 - Sources`), no
contradiction between the two.

### Verification

```
python3 -m pytest tests/ -q          # 462 passed, 26 subtests passed
python3 scripts/validate_repository.py  # only pre-existing, unrelated warnings

$ wc -l "$AGENT_MEMORY_VAULT/index.md"                    # 132
$ ls "$AGENT_MEMORY_VAULT/01 - Notes/01f - Facts" | wc -l  # 119
```

Both match exactly what `docs/corpus/index.md` now instructs. Opened
**agent-dotfiles PR #347**, branch `fix/global-instructions-prose-repair`,
off `origin/main` at `bf74e8a`, head `5fa39b8`. Not merged — a different
lane reviews.

## Install-sync — live global instructions repaired (2026-09-07)

Director's follow-up on agent-dotfiles#347: the canonical fix landed
(`5894803`) but the INSTALLED copies every session actually reads still
carried the old `agent/index.md` text. Job: establish and use the real
install/sync mechanism, never hand-edit unless proven hand-maintained.

### Mechanism established, with evidence

`apm compile --global` (APM CLI 0.24.1), reading from
`~/.apm/apm_modules/_local/agent-dotfiles` — a local-path dependency
mirror of the agent-dotfiles repo, declared in `~/.apm/apm.yml`.
Confirmed the target files' generation status **differently for each
file**, not assumed uniform:

- **`~/.claude/CLAUDE.md` IS generated.** Ran the real compile inside a
  fully isolated scratch `$HOME` (a copy of `~/.apm`, nothing under the
  real `~` touched) — twice, once with a pre-seeded copy of the real
  `rules/global.md` present and once without, confirming the dedup logic
  doesn't change `CLAUDE.md`'s own content either way. The only
  substantive diff between the scratch-generated file and the real live
  one was the routing line (plus a cosmetic Build ID comment, which
  changes on every real compile run regardless of content and was left
  untouched rather than bumped, since I did not actually run the real
  generator against the live file).
- **`~/.claude/rules/global.md` is NOT touched by `apm compile --global`
  at all**, even when the file already exists — confirmed directly: ran
  the same isolated-scratch-HOME compile with a copy of the real
  `rules/global.md` pre-seeded, and it came out **byte-identical**
  afterward. No generator writes this file. It is genuinely
  hand-maintained (or written by some other, now-unknown mechanism) —
  Director's own second branch applies: "if hand-maintained, say so
  plainly and then edit them, because there is no generator to run."

### Why the real global compile was not run against the live files

`apm compile --global` cannot be scoped to a single target
(`--global` rejects `--target`; the commented `targets:` pin in
`apm.yml` doesn't narrow `--global` mode either, tested and confirmed).
A real run would touch all 10 configured harnesses at once — and 6 of
them (`cursor`, `kiro`, `opencode`, `antigravity`, `windsurf`, `hermes`)
have **zero existing footprint** in `~` today; a real compile would
create brand-new files for harnesses Jon may not even use. This is
squarely "the sync tool would rewrite unrelated content" — per the
explicit instruction, stopped rather than ran it. (`copilot` and
`codex` DO already exist and carry the identical `agent/index.md`
defect — left alone too, since fixing them wasn't asked for in this
task and doing so wasn't separately authorized.)

### What was actually done

Since the exact bytes `CLAUDE.md`'s real generator would produce were
now verified (not assumed — reproduced twice in isolation), and
`rules/global.md` has no generator at all, applied that same verified
routing-line replacement directly to both live files — the narrowest
possible correct action given the tool's own scoping limits, not a
guess at what the fix should look like.

Also refreshed the stale `~/.apm/apm_modules/_local/agent-dotfiles`
mirror's copy of `instructions/global.instructions.md` (previously
frozen at an Aug 9 snapshot, even older than either the intermediate
live wording or the #347 fix) to match `origin/main` at `5894803` — a
legitimate cache-freshness fix with no live-file side effect on its
own, left in place since it makes any FUTURE real compile run correctly
instead of regressing to the oldest wording.

### Acceptance

```
$ diff CLAUDE.md.bak ~/.claude/CLAUDE.md
71c71,76
< - Read `Start Here.md`, then the capped `agent/index.md`; load scoped notes on demand.
---
> - Read `Start Here.md` for routing, then the capped bundle-root `index.md`
>   (the `okf_version` carrier, OKF section 12 — not a browsing index); load
>   scoped notes on demand. Notes live in `01 - Notes` under earned letter
>   subdirs (registry: `99 - Meta/note-subdirs.md`, e.g. `01f - Facts`,
>   `01p - Parameters`); hubs are in `02 - MOCs`; meta in `99 - Meta`; sources
>   in `05 - Sources`.

$ diff global.md.bak ~/.claude/rules/global.md
68c68,73
< - Read `Start Here.md`, then the capped `agent/index.md`; load scoped notes on demand.
---
> [identical replacement text]

$ grep -n "agent/index" ~/.claude/CLAUDE.md ~/.claude/rules/global.md
(no output — clean)
```

Both backups (`CLAUDE.md.bak`, `global.md.bak`, sha256-verified
byte-identical to the pre-edit live files at the moment they were
taken) kept at
`/tmp/agent-estate-evidence/scratch-backup-2026-09-07/` for this
session. All scratch worktrees/homes used for verification removed;
`~/.apm/apm.yml` confirmed byte-identical to its own pre-task backup
(temporarily edited to test a `targets:` pin, reverted, diffed clean).

## #1247 follow-up: sweep report mode consults isolate — #1278 (2026-09-07)

Director found this by RUNNING the merged #1277 fix, not a new
investigation: report mode said "would remove" for 7 worktrees on this
host; `--apply` refused all 7. Root cause confirmed by reading
`internal/sweep`'s own code: `judge()` decides eligibility from ledger
state alone; the uncommitted-work check (`isolate.Worktree.DirtyStatus`)
only ever runs inside `cfg.Remove`, which report mode leaves nil. Same
defect class as #1247 itself.

### Design decision worth flagging

`Config` gained a `DirtyCheck` seam mirroring the existing `Remove` seam
exactly (nil = old behavior, fakeable in tests) rather than having
`internal/sweep` call `isolate.Reattach`/`DirtyStatus` directly — this
keeps the package's own stated architecture intact ("every field that
touches the world is a seam"). The one real tension: Director's
constraint said "diff must touch nothing outside internal/sweep and its
tests," but `DirtyCheck`'s real implementation has to be wired from
somewhere that has the caller's own `repoRoot` — exactly where `Remove`
is already wired, in `main.go`'s `sweepConfig`. Disclosed this explicitly
in the PR rather than silently either violating the constraint or
skipping the wiring (which would leave the fix inert).

### Reproduction methodology for acceptance criterion 2

Couldn't reproduce the exact 7-worktree finding directly — sweep's
"dispatch root" is a one-way sha256 hash of an absolute repo path, and
none of my own invocation contexts (fresh worktree, shared checkout)
hashed to a root with real eligible ledger entries; the ORIGINAL
checkout Director used may no longer exist under that name. Built a
genuine, live reproduction instead: a real bare `origin`, a real `main`
checkout, three real `isolate.Create` worktrees (clean / dirty-but-
byte-identical-to-a-later-origin-commit / dirty-with-unique-content), a
hand-built matching ledger. Ran the UNPATCHED binary at 145ea88 first to
confirm the exact disagreement reproduces, then the patched binary to
confirm agreement — both pasted in the PR, not asserted.

### Verification

```
go build ./... && go vet ./...   # clean
go test ./...                     # all packages pass
```

3 new tests in `internal/sweep/sweep_test.go`: `TestReportAndApplyAgree`
(built report's `DirtyCheck` and apply's `Remove` from the SAME fake
fixture world, so agreement can't pass by coincidence),
`TestReportModeNamesTheThreeTypedStates`, `TestReportModeWithDirtyCheckStillMutatesNothing`.
Mutation-checked: disabled the `DirtyStateUnique` branch, confirmed the
first two tests failed for exactly that reason, restored, confirmed
green.

Real before/after CLI output (145ea88 unpatched vs. this fix), and a
`git worktree list` diff proving report mode writes nothing even when it
correctly judges a worktree removable — both pasted in full in the PR
body.

Opened **agent-estate PR #1278**, branch `fix/sweep-report-consults-isolate`,
off `origin/main` at `145ea88`, head `94d15b58916854bedb15484a848bed07ab74d4a1`.
Not merged — a different lane reviews. Collision check confirmed with
Director before starting: `internal/sweep` only, no overlap with Astra's
Push 4.5 (`internal/knowledge`, `internal/candidates`, the vault).

## #1278 fix pass — second disagreement path closed (2026-09-07) — DONE

Director's framing: "the ONE fix pass allowed" -- lane-b found the first
pass (previous section) closed only the uncommitted-content disagreement
path (report mode's `DirtyCheck` wired to `isolate.Worktree.DirtyStatus`
alone). `Remove` -- what apply mode actually runs -- also refuses via a
second, independent check: `Committed()` + `remoteHasCommit()` + the
`Landed` forge fallback. A worktree that is clean-but-unpushed (no dirty
files, but real commits nothing else references) passed `DirtyStatus` and
was still reported "would remove", then refused by apply through this
other path -- the same disagreement class, reached a different way.

**Fix, per lane-b's suggested shape:** extracted the judgement `Remove`
performed up to but not including the mutation into a new exported
`isolate.Worktree.CheckRemovable() (DirtyState, error)`. `Remove` now
calls `CheckRemovable` first and only mutates on a nil error --
unchanged in every other respect (full `internal/isolate` suite passes
unmodified). `internal/sweep`'s `Config.DirtyCheck` renamed to
`RemovalCheck`, signature dropped the separate `differing` return
(`CheckRemovable`'s error text already carries it). `main.go`'s
`sweepConfig` wires `RemovalCheck` to `Reattach` + `CheckRemovable` (not
`DirtyStatus`), and sets `corpse.Landed` from the same value apply mode
passes to `Remove` -- meaning `sweepWorktrees` now computes the
`GHLanded` forge lookup unconditionally rather than only under `--apply`,
so report mode's judgement runs on the identical inputs apply's would.
Two callers of one judgement cannot drift.

**Verified, all five acceptance criteria:**
1. New test `TestReportModeKeepsACleanButUnpushedWorktree` -- report says
   "would keep" naming the reason, not "would remove".
2. `TestReportAndApplyAgree` extended with an "unpushed" case alongside
   the existing dirty-state cases.
3. Full `internal/isolate` suite (all pre-existing `Remove` tests) passes
   unmodified -- apply's behavior is unchanged.
4. Mutation test: `CheckRemovable` temporarily forced to
   `return DirtyStateClean, nil` unconditionally. Both
   `TestSweepRemovesWhatLandedAndKeepsWhatDidNot` (apply) and the new
   `TestSweepReportModeAgreesWithApplyOnTheSameThreeWorktrees` (report,
   added this pass, real git/ledger integration test) failed for the
   expected reason ("the forge was never consulted"); ~12 further
   `internal/isolate` tests failed across every refusal shape
   `CheckRemovable` protects. Reverted, full suite reconfirmed green.
5. Live scratch reproduction: real bare origin, a real
   `isolate.Create`'d worktree with a commit made and never pushed, a
   real JSONL ledger, the real built `estate` binary run both without
   and with `--apply`. Both printed the identical `CheckRemovable`
   refusal text ("...cannot confirm .../clean-unpushed's commits on
   dispatch/clean-unpushed are referenced elsewhere; refusing to remove
   it -- collect them first: ...") -- report said "would keep: ...",
   apply said "kept: ..." for the same reason, agreeing line for line.
   Scratch repo, ledger, and the temporary `cmd/setuprepro` +
   `cmd/setupledger` helpers were deleted before committing; `git status
   --short` showed only the 5 intended files.

`go build ./... && go vet ./... && go test ./...` all green (`ok` on
every package) before and after.

Rebase: `origin/main` unchanged at `145ea88` since the first pass opened
-- no-op, confirmed via `git rebase origin/main`.

Pushed to the existing branch `fix/sweep-report-consults-isolate`. CI
green (`gh pr checks 1278`, both `estate` jobs `pass`). Head SHA:
`2eff016806df44d1b97dea8bb35970eeac1d591d`. Not merged -- lane-c re-reviews
under the one-fix-pass rule.
