# P12 Phase 1 — agent-estate docs/ disposition table

Author: agent-estate:2 (lane-b). Judgment only — **zero file moves made**.
Every file under `docs/` in `origin/main` at `8ba75ac` (agent-estate#1282
merged) is listed below with exactly one disposition, a one-line reason,
and a consumer check (`grep -rn` for the file's basename and, where the
basename is generic, its qualified path — across this repo, `agent-dotfiles`,
`Skills`, and the vault). Every hit found is listed; none summarized away.
Consumer greps for `.md`/`.go`/`.py`/`.yml`/`.json` files, excluding `.git/`
and (in `agent-dotfiles`) ephemeral `.worktrees/`/`.claude/worktrees/`/
`.claude/backups/` dirs, which are not real git-tracked consumers.

Dispositions used: **canonical** (current, actively read/routed-to truth) |
**historical** (retired/point-in-time, correctly kept but not "live truth") |
**research** (none found in this repo's `docs/` — noted where relevant) |
**relocate-out-of-docs** (state, not documentation) | **DELETE-CANDIDATE**
(listed for Jon, never executed by this table or its author).

No file below is disposed **research** — nothing under this repo's `docs/`
reads as exploratory/finding-oriented in the sense `docs/spend-observation.md`
almost is (see its own row: kept canonical because it is actively cited as
the operative source of truth for harness cost behavior, not archived
research). If a reviewer disagrees on any single row, that is exactly what
cross-lane review of this table is for.

Full-text duplicate check run now (Phase 2's rule 3, sanity-checked early
since it's cheap): `find docs -name "*.md" -exec sha256sum {} \; | sort |
awk '{print $1}' | uniq -d` → **empty, zero duplicates**. No DELETE-CANDIDATE
arises from duplication.

---

## docs/ci-rules-retired.md

**Disposition: historical.** Content is explicitly about CI rules retired
with the shell supervisor (`Written 2026-09-02`) — this is a point-in-time
record of what stopped applying and why, not current operative truth, but
it is deliberately kept (not stale) per the daemon doc's own framing.

Consumers:
- `AGENTS.md:105` — routing table link `[docs/ci-rules-retired.md](docs/ci-rules-retired.md)`
- `docs/orientation/daemon.md:18,45` — two relative links `../ci-rules-retired.md`
- `docs/orientation/conventions.md:65` — one relative link `../ci-rules-retired.md`
- `src/estate/cmd/goldenquery/main.go:884` — comment, non-functional
- `src/estate/internal/knowledge/goldenset/cases.json:112-113` — `expected_identifier` keyed to this exact path + anchor slug; a golden-query test fixture
- `src/estate/internal/knowledge/goldenset/natural_cases.json:186-188` — same, natural-language stratum, three lines (`expected_identifier`/`rationale`/`note`)
- `docs/tick-log.jsonl:10` — one historical artifact-field mention (`"docs/ci-rules-retired.md + green estate-ci"`) — immutable log entry, not a live consumer, needs no update if moved
- vault / agent-dotfiles / Skills: none

## docs/decisions/2026-09-06-knowledge-architecture-merge-exception.md

**Disposition: historical.** Already lives under `docs/decisions/` — a
decision record (ADR-shaped) for one specific, dated, one-time merge
exception Jon authorized. Inherently point-in-time by genre, correctly
kept, not current operative policy (the operative policy is
`docs/orientation/conventions.md`, which this file itself says explicitly).

Consumers:
- `src/estate/internal/knowledge/goldenset/natural_cases_test.go:18` — count-assertion comment naming this file as one of the 27 cases' provenance
- `src/estate/internal/knowledge/goldenset/natural_cases.json:246-248` — `expected_identifier`/`rationale`/`note`, golden-query fixture
- `docs/orientation/conventions.md` — referenced FROM this file (four lines, 4/10/58/65), not the reverse; already counted under `conventions.md`'s own row as content, not a structural link back
- vault / agent-dotfiles / Skills: none

## docs/director-brief.md

**Disposition: canonical.** The Director's own standing operational brief
— actively read by the running cron loop's own instructions ("You are the
Director of this estate...").

Consumers:
- `AGENTS.md:108` — routing table link
- `docs/phase-plan.md:164` — prose cross-reference (byte-count claim)
- `docs/director-loop.md:35` — prose cross-reference ("Read docs/director-brief.md §3...")
- `src/estate/cmd/goldenquery/main.go:881` — comment, non-functional
- `src/estate/internal/knowledge/goldenset/natural_cases.json:114-116,206-207` — two `expected_identifier` fixture entries + rationale/note
- `src/estate/internal/tick/tick.go:4,789` — two comments citing it as the spec this package implements (non-functional string, but the PACKAGE'S OWN stated reason for existing)
- vault: `04 - Projects/agent-estate.md` mentions it in prose (a project note, not a functional consumer)
- agent-dotfiles / Skills: none

## docs/director-loop.md

**Disposition: canonical.** Describes the mechanism the live Director cron
loop actually runs today.

Consumers:
- `AGENTS.md:108` — routing table link (paired with director-brief.md)
- `docs/tick-log.jsonl:3` — one historical artifact-field mention (immutable, not a live consumer)
- `src/estate/main.go:2360,2498` — one `fmt.Printf` STRING LITERAL citing this path in CLI output text (`"...cron cadence, not work duration -- see docs/director-loop.md"`) — this is user-facing output text, not a functional path resolution, but it IS a literal string a reader would follow
- `src/estate/cmd/goldenquery/main.go:883` — comment, non-functional
- `src/estate/internal/knowledge/goldenset/natural_cases.json:159-161` — fixture entry
- `src/estate/internal/tick/tick.go:74` — comment
- vault: `04 - Projects/agent-estate.md` prose mention
- agent-dotfiles / Skills: none

## docs/knowledge-system.md

**Disposition: canonical.** Describes `estate knowledge`'s compiled index
as it exists today; actively routed to and actively cited BY PRODUCTION
CODE as the citation an agent is told to go read.

Consumers — **highest functional risk in the loose-doc set**:
- `AGENTS.md:106` — routing table link
- `.claude/skills/knowledge-session/SKILL.md:28` — prose reference
- `docs/knowledge-workflow.md:50,108,190,198` — four cross-references, one with an anchor slug (`#how-does-knowledge-retrieval-work-in-this-repo--...`)
- `src/estate/knowledge_grounding_scoping_test.go:28` — **`if !strings.Contains(got, "docs/knowledge-system.md")`** — a test asserting PRODUCTION-GENERATED grounding text contains this exact literal path string
- `src/estate/main.go:211,218,248,305,326` — five hits: three are comments, but **:305 and :326 are STRING LITERALS inside the actual grounding-text-building code** (`"...or see \`docs/knowledge-system.md\`'s..."`, `"see \`docs/knowledge-system.md\` for the full scoping rules"`) — this is the production code the test above pins. Moving this file without updating these two literals AND the test would leave a live citation pointing at a path that no longer exists.
- `src/estate/cmd/goldenquery/main.go:882` — comment
- `src/estate/internal/knowledge/query.go:1237` — comment
- `src/estate/internal/knowledge/goldenset/natural_cases.json:141-143` — fixture entry
- vault: `99 - Meta/index-contract.md`, `04 - Projects/agent-estate.md` — prose mentions
- agent-dotfiles / Skills: none

## docs/knowledge-workflow.md

**Disposition: canonical — OWNED BY PHASE 4, excluded from this phase's
move execution regardless of table disposition, per explicit instruction.**
Listed here only to satisfy "every file under docs/" completeness; Phase 2
must not move or edit it, and Phase 4 (live repo pointers) already has a
standing edit planned against its disclosure-guidance section.

Consumers (for the record, not to be acted on by Phase 2):
- `AGENTS.md:107` — routing table link
- `.claude/skills/knowledge-session/SKILL.md:9` — a relative link `../../../docs/knowledge-workflow.md` (three levels up — would break silently if this file's depth changed, a real reason to leave it exactly where it is)
- `src/estate/internal/candidates/catalogue_test.go:203,214,218,222,228,249,263` — seven hits; this path is reused as an EXAMPLE repo-destination string in `PublishRepo` test fixtures. It is not asserting the file's real content, only reusing a real, stable-looking path as plausible fixture data — moving the file would not break these tests (they don't read the real file), but the string would then describe a path that no longer exists, which is exactly the kind of stale-fixture smell worth a human's eye, not a mechanical fix.
- `src/estate/internal/knowledge/goldenset/natural_cases_test.go:18`, `natural_cases.json:237-239` — fixture entries
- vault: `04 - Projects/agent-estate.md` prose mention
- agent-dotfiles / Skills: none

## docs/orientation/conventions.md

**Disposition: canonical.** Already correctly subdirected. Binding merge/
review convention, actively enforced (the one-fix-pass rule this very PR
review chain has been running under).

Consumers:
- `AGENTS.md:73,102` — two links (routing table + earlier body reference)
- `docs/decisions/2026-09-06-knowledge-architecture-merge-exception.md:4,10,58,65` — four prose cross-references (the merge-exception doc explains itself against this file's own protocol)
- `src/estate/internal/knowledge/goldenset/natural_cases_test.go:18`, `natural_cases.json:228-229,249` — fixture entries, including a `target_text` field that QUOTES this file's own prose verbatim (would go stale, not break, if the quoted section's heading changed — not a path issue)
- **False positives, not real consumers**: `src/tui/cmd/estate/memgraph.go:12`, `src/tui/internal/knowledge/index.go:5,13`, `src/tui/internal/knowledge/fact.go:12`, `src/tui/internal/knowledge/index_test.go:15`, `src/tui/internal/memgraph/view.go:34` all match on the substring `conventions.md` but reference a DIFFERENT file entirely — the vault's own `memory-conventions.md` (an Agent Memory fact, not this repo doc). Listed here explicitly so the grep hit is accounted for and not silently dropped, not because it's a real dependency.
- vault / agent-dotfiles / Skills: none (the vault's `memory-conventions.md` above is a distinct file, not this one)

## docs/orientation/daemon.md

**Disposition: canonical.** Already subdirected; routes daemon-touching
tasks correctly today.

Consumers:
- `AGENTS.md:77,100` — two links
- `docs/ci-rules-retired.md` — referenced FROM this file (already counted under that row)
- `src/estate/internal/knowledge/goldenset/natural_cases.json:60` — fixture entry
- vault / agent-dotfiles / Skills: none

## docs/orientation/go-only.md

**Disposition: canonical.** Binding Go-only rule, actively cited.

Consumers:
- `AGENTS.md:67,104` — two links
- `src/estate/internal/knowledge/goldenset/natural_cases.json:33` — fixture entry
- vault / agent-dotfiles / Skills: none

## docs/orientation/invariants.md

**Disposition: canonical.** The ten numbered invariants; binding, actively
cited.

Consumers:
- `AGENTS.md:79,101` — two links
- `src/estate/internal/knowledge/goldenset/natural_cases.json:15,24` — two fixture entries
- vault / agent-dotfiles / Skills: none

## docs/orientation/tui-arrival.md

**Disposition: canonical.**

Consumers:
- `AGENTS.md:103,114` — two links, **plus a pre-existing naming inconsistency worth flagging for Phase 3 (not fixed here, AGENTS.md is out of scope for Phase 1)**: `AGENTS.md:115` refers to this file as `` `docs/tui-arrival.md` `` (missing the `orientation/` segment) in prose, one line after correctly linking it in full at `:103`/`:114`. Not a broken link (no markdown link syntax there, just a backtick-quoted path in prose), but a real, pre-existing small defect for whoever does Phase 3's surgical AGENTS.md refresh to notice.
- `src/estate/internal/knowledge/goldenset/natural_cases.json:6,42,51` — three fixture entries
- vault / agent-dotfiles / Skills: none

## docs/phase-plan.md

**Disposition: canonical — HIGH-RISK relocate/rename target, flagged for
Phase 3's judgment, not moved here.** Describes current roadmap phase
status; the daemon's own runtime code reads it functionally, not just
prose-cites it.

Consumers — **second-highest functional risk after knowledge-system.md**:
- `AGENTS.md:109` — routing table link
- `docs/tick-log.jsonl:1,19,21,22` — **four historical artifact-field entries** citing this exact path (immutable log, not a live consumer)
- `docs/director-loop.md:35,155` — two prose cross-references
- `docs/director-brief.md:12` — one prose cross-reference
- `src/estate/tick_resolve.go:196` — comment
- **`src/estate/main.go:2241,2389,2471` — THREE call sites of `tick.KnownPhases("docs/phase-plan.md")` — a LITERAL RUNTIME PATH STRING that is actually opened and read from disk at every `estate tick record`/`check`/`escalate` invocation that validates a phase-item token.** This is the same class of functional risk the tick-log.jsonl trap names, just for a file this table disposes canonical rather than relocate — moving or renaming `docs/phase-plan.md` without updating these three call sites breaks live phase-token validation immediately.
- `src/estate/cmd/goldenquery/main.go:882` — comment
- `src/estate/internal/tick/tick_test.go:311,336,340,355,356` — **five hits, several inside VALIDATION LOGIC FIXTURES**: `tick_test.go:311,336` are map/slice literals of "known real paths" used to test `Validate()`'s token-resolution; `:340,355-356` construct test artifact strings and a resolver closure keyed to the literal `"docs/phase-plan.md"`. These are tests, not runtime, but they'd need updating in lockstep with the main.go call sites above if this file ever moves — same PR, not a separate concern.
- `src/estate/internal/harness/harness.go:52` — comment
- `src/estate/internal/tick/tick.go:518` — comment
- `src/estate/internal/knowledge/goldenset/natural_cases.json:150-152,162` — fixture entries, one `target_text` quoting `docs/director-loop.md`'s prose which itself mentions this path
- vault: `04 - Projects/agent-estate.md` prose mention
- agent-dotfiles / Skills: none

## docs/product/PRD.md

**Disposition: canonical.** Product intent, actively maintained.

Consumers:
- `docs/orientation/tui-arrival.md:49` — cross-reference to the SIBLING `docs/tui/PRD.md`, not this file — listed because the basename search matched it; this file (`docs/product/PRD.md`) has no direct link FROM tui-arrival.md, only its sibling does
- `docs/tui/SPEC.md:15,26,279,405` — four hits, but these are **all self-referential mentions of `docs/tui/PRD.md` using the shorthand `docs/PRD.md`** (an existing relative-path assumption within the `docs/tui/` sibling pair, not references to `docs/product/PRD.md`) — see this same ambiguity noted under `docs/tui/PRD.md`'s own row
- `docs/product/SPEC.md:9` — one cross-reference (`Intent lives in \`PRD.md\`` — bare filename, sibling-relative, correctly resolves within `docs/product/`)
- `docs/tui/PRD.md:15` — self-reference via the sibling-shorthand convention (see `docs/tui/PRD.md`'s row)
- `src/estate/cmd/goldenquery/main.go:882` — comment (names `docs/tui/PRD.md`, not this one — basename collision)
- `src/estate/internal/knowledge/goldenset/natural_cases.json:96-97,105-106` — two fixture entries for THIS file specifically (`docs/product/PRD.md#...`)
- vault / agent-dotfiles / Skills: none
- **Note**: `PRD.md` is a generic basename shared with `docs/tui/PRD.md`; every grep hit above was individually read for which file it actually names before being attributed to this row.

## docs/product/SPEC.md

**Disposition: canonical.** What is actually built, per its own opening
line; actively maintained against the real tree.

Consumers:
- `README.md:55` — names the SIBLING `docs/tui/SPEC.md`, not this file (basename collision, listed for completeness)
- `docs/tui/PRD.md:15,27,100,120,244` — five hits, all self-referential to `docs/tui/SPEC.md` via the `docs/SPEC.md` shorthand (sibling convention, not this file)
- `docs/tui/SPEC.md:15` — self-reference (sibling convention)
- `docs/orientation/tui-arrival.md:50,73` — two hits, name `docs/tui/SPEC.md`, not this file
- `reference/scripts/supervisor/watchdog_notify.py:3`, `acp_transport.py:3`, `recycle.py:3`, `sleepcheck.py:3` — four Python reference-script docstrings citing `docs/SPEC.md §15` — ambiguous shorthand, but `reference/` is explicitly non-canonical per `docs/orientation/go-only.md` ("`reference/` is reference material, not a codebase") and these predate the TUI/estate merge, so this most likely names the pre-merge `agent-supervisor`'s own doc, not either of today's two `SPEC.md` files — flagged as ambiguous rather than guessed
- `reference/scripts/supervisor/laneview/README.md:42` — explicitly names `docs/product/SPEC.md` — a real hit for THIS file
- `src/estate/internal/knowledge/query.go:1238` — comment, explicitly names `docs/product/SPEC.md`
- `src/estate/internal/knowledge/docs_test.go:43,60` — synthetic fixture in a `t.TempDir()`, builds its own throwaway `docs/product/SPEC.md`-shaped path to test `repoDocsSource`'s generic `docs/**/*.md` walk; **confirmed by reading `repoDocsSource` itself (`docs.go:39`): it walks `repoRoot/docs/**/*.md` recursively with no hardcoded subdirectory names**, so this fixture does not pin any real dependency on this file's current location — moving files under `docs/` does not require touching the indexer's own source code, only the golden-set fixtures that assert specific `expected_identifier` values (see rows above)
- `src/estate/internal/knowledge/goldenset/natural_cases.json:78-79,87-88` — two fixture entries for THIS file specifically
- `src/tui/internal/connectors/connector.go:10` — comment, ambiguous shorthand `docs/SPEC.md`, most likely names `docs/tui/SPEC.md` given the file's own subject matter (connectors, TUI-side)
- agent-dotfiles / Skills: none

## docs/reviewer-value.md

**Disposition: canonical.** Actively-maintained finding on the review
convention's own cost/value.

Consumers:
- `AGENTS.md:110` — routing table link
- `docs/orientation/conventions.md:28` — one relative link `../reviewer-value.md`
- `docs/tick-log.jsonl` — this file's OWN prose (`:100` in its rendered content, not the jsonl file's own lines) mentions `docs/tick-log.jsonl` — already counted as a consumer under `tick-log.jsonl`'s row, not a reference TO this file
- `src/estate/cmd/goldenquery/main.go:883` — comment
- `src/estate/internal/knowledge/goldenset/natural_cases.json:177-179` — fixture entry
- vault / agent-dotfiles / Skills: none

## docs/spend-observation.md

**Disposition: canonical.** What a turn actually costs, harness by
harness — actively cited as the operative source of truth for spend/cost
code, not archived research, despite reading like a "finding."

Consumers — **most Go-comment references of any single file (13)**, all
non-functional (prose citations, not path resolution):
- `AGENTS.md:111` — routing table link
- `src/estate/internal/harness/harness.go:71,226,230,284,365,399` — six comments
- `src/estate/internal/harness/harness_test.go:165,217,244,253,286` — five comments
- `src/estate/cmd/goldenquery/main.go:883` — comment
- `src/estate/internal/knowledge/goldenset/natural_cases.json:168-170` — fixture entry
- `src/estate/internal/ledger/ledger.go:122` — comment
- `src/estate/internal/tick/tick.go:110` — comment
- `src/estate/internal/spend/spend.go:8,282` — two comments
- `src/estate/internal/spend/spend_test.go:35` — comment
- vault / agent-dotfiles / Skills: none

## docs/tick-escalations.jsonl

**Disposition: relocate-out-of-docs.** Live state, not documentation —
matches the director's own carried-over framing exactly. **NOT moved by
this table; Phase 2/3 must weigh the full cost below before acting.**

What repointing costs (stated in full, per instruction, not a one-word
verdict):
- `src/estate/internal/tick/tick.go:48` — `const DefaultEscalationPath = "docs/tick-escalations.jsonl"`, resolved by `EscalationPath()` against `ESTATE_TICK_ESCALATION_LOG` (override) else this constant, itself resolved against the PROCESS CWD wherever `estate tick escalate` runs
- **The live Director cron loop is running right now and calls `estate tick escalate`, appending to this exact path from the shared checkout's own cwd — independent of any branch/worktree.** A `git mv` in a PR only renames the file in git history; the shared checkout's actual on-disk file could accumulate new, uncommitted lines between this PR's merge and the shared checkout's next sync. If that sync is a `git pull`/fast-forward, git generally refuses to silently overwrite a path with local uncommitted changes — a live-appended file at the old path colliding with a git-history rename to a new path is a real, not hypothetical, coordination hazard.
- `src/estate/tick_check_discloses_reclaimable_test.go:262` — uses `"tick-escalations.jsonl"` as a scratch-fixture basename (via `t.TempDir()`), not the real path; unaffected by any real move
- `src/estate/internal/tick/tick_escalation_test.go:14` — same, scratch fixture, unaffected
- `docs/director-loop.md:92`, `docs/director-brief.md:115` — two prose cross-references describing this file's role (would need updating to the new path, once chosen, as normal doc maintenance — not itself risky)
- No `src/tui` consumer found for the escalation log specifically (unlike `tick-log.jsonl` below) — grep confirms zero hits under `src/tui/`

## docs/tick-log.jsonl

**Disposition: relocate-out-of-docs.** Live state, not documentation.
**NOT moved by this table.** This is the file the director's brief named
explicitly, and the consumer check below shows the blast radius is WIDER
than a single `tick.Path()` — there are **two independent subsystems**
that hardcode this path, not one.

What repointing costs, in full:
- `src/estate/internal/tick/tick.go:32` — `const DefaultPath = "docs/tick-log.jsonl"`, resolved by `Path()` against `ESTATE_TICK_LOG` (override) else this constant, against the PROCESS CWD — same live-loop-write-window hazard as the escalations file above, for `estate tick record`
- `src/estate/tick_check_discloses_path_test.go` — **the pinning test named in the brief**: asserts `tick check` discloses the ABSOLUTE path it actually resolved (via `ESTATE_TICK_LOG` override in the test, not the bare default — the test itself does not hardcode `docs/tick-log.jsonl` as a literal string anywhere in its assertions, only as a doc-comment description of the defect it reproduces, `:13`). Repointing `DefaultPath` does not require editing this test's assertions, only confirming its `ESTATE_TICK_LOG`-override path still resolves correctly (it would, unchanged) — the risk here is entirely about the LIVE FILE, not this test.
- **`src/tui/cmd/estate/main.go:87`** — `estateTickLog := flag.String("estate-tick-log", "docs/tick-log.jsonl", ...)` — **a SECOND, independent hardcoded default**, in the TUI's own entrypoint, used to show live tick status on its Home screen
- `src/tui/cmd/estate/tickpath.go:37,43,53` — the TUI's own `resolveTickLogPath` helper and its doc comments, built around this same default
- `src/tui/cmd/estate/tickpath_test.go` — **8 hits** (`:21,25,32,52,75,80,103,136,204,231` — several lines), the TUI's own pinning tests for its independent path-resolution logic, hardcoding the literal `"docs/tick-log.jsonl"` string directly in test assertions (unlike the estate-side pinning test, these DO hardcode the literal path)
- `src/tui/internal/shell/frame_capture_test.go:17` — comment, a documented env-var example (`FRAME_TICKS=docs/tick-log.jsonl`)
- `src/tui/internal/estatus/estatus.go:89` — comment
- `src/tui/internal/estatus/estatus_test.go:40,53,117,148,247` — five hits, all use `"tick-log.jsonl"` as a scratch-fixture basename via a local `write()`/`missing()` helper (need to confirm these resolve against `t.TempDir()`-style scratch paths, not the real repo path — read directly: yes, `Read(write(t, "ledger.jsonl"), write(t, "tick-log.jsonl"))` — `write` is a local test helper writing to a temp location, unaffected by a real move)
- `src/estate/tick_resolve_test.go:235`, `tick_observed_spend_test.go` (×4), `tick_check_discloses_reclaimable_test.go` (×8), `internal/tick/tick_test.go` (×5) — all use `"tick-log.jsonl"` as a scratch-fixture basename via `filepath.Join(t.TempDir(), ...)` or similar — confirmed unaffected by a real move, not real dependents on the live path
- `src/estate/tick_resolve.go:227,262` — two comments
- `docs/reviewer-value.md:100`, `docs/phase-plan.md:27`, `docs/director-loop.md:81`, `docs/director-brief.md:94,106,116` — six prose cross-references across FOUR other canonical docs, all describing this file's role/format (normal doc maintenance once a new path is chosen, not itself risky)
- `src/estate/internal/knowledge/goldenset/natural_cases.json:209` — one `target_text` field quoting `docs/director-brief.md`'s own prose, which itself quotes the append-format instructions naming this path — a fixture consequence of `director-brief.md`'s content, not a structural dependency on the jsonl file's location
- vault / agent-dotfiles / Skills: none

**Net assessment for Phase 2/3's judgment**: relocating `docs/tick-log.jsonl`
requires coordinated changes to `internal/tick.DefaultPath` AND
`src/tui/cmd/estate`'s own independent default + `resolveTickLogPath`, plus
rewriting 8 hardcoded-literal assertions in `tickpath_test.go`, plus the
live-loop-write-window coordination hazard described above (shared with the
escalations file). This is real, multi-subsystem work, not a one-line
constant change — Phase 2/3 should budget it as such rather than folding it
into an otherwise-mechanical move pass.

## docs/tui/PRD.md

**Disposition: canonical.** TUI product intent.

Consumers:
- `docs/orientation/tui-arrival.md:49` — direct link, correctly full-path (`docs/tui/PRD.md`)
- `docs/tui/PRD.md:15` (self) — opens with "Sibling documents referenced below (`docs/PRD.md`, `docs/SPEC.md`, ...)" — **this file refers to itself and its sibling using bare, one-level-shorter shorthand paths** (`docs/PRD.md` instead of `docs/tui/PRD.md`) that only resolve correctly by assuming the reader is already inside `docs/tui/` — a pre-existing convention, not a bug, but worth Phase 3 knowing about since it means these two files' cross-references are NOT full qualified paths today
- `docs/product/SPEC.md:9` — names the sibling `PRD.md` bare (its own sibling, `docs/product/PRD.md`, not this one — same shorthand convention within `docs/product/`)
- `docs/tui/SPEC.md:15,26,279,405` — four self/sibling references using the same shorthand
- `src/estate/cmd/goldenquery/main.go:882` — comment, correctly full-path
- `src/estate/internal/knowledge/goldenset/natural_cases.json:132-134,195-196` — two fixture entries for THIS file specifically
- vault / agent-dotfiles / Skills: none

## docs/tui/SPEC.md

**Disposition: canonical.** TUI technical design as it exists.

Consumers:
- `README.md:55` — direct link, correctly full-path
- `docs/tui/PRD.md:15,27,100,120,244` — five self/sibling references, shorthand convention (see `docs/tui/PRD.md`'s row)
- `docs/tui/SPEC.md:15` (self) — same shorthand convention
- `docs/orientation/tui-arrival.md:50,73` — two direct links, correctly full-path
- `reference/scripts/supervisor/sleepcheck.py:3`, `watchdog_notify.py:3`, `acp_transport.py:3`, `recycle.py:3` — four Python docstrings, ambiguous shorthand `docs/SPEC.md §15` — see the ambiguity note under `docs/product/SPEC.md`'s row; most likely pre-merge `agent-supervisor` doc, not confidently this file either
- `reference/scripts/supervisor/laneview/README.md:42` — names `docs/product/SPEC.md`, not this file (already counted there)
- `src/estate/cmd/goldenquery/main.go:881` — comment, correctly full-path
- `src/estate/internal/knowledge/goldenset/natural_cases.json:123-125,217-218` — two fixture entries for THIS file specifically
- `src/estate/internal/knowledge/docs_test.go:43,60` — synthetic fixture, unaffected (see `docs/product/SPEC.md`'s row for the `repoDocsSource` confirmation)
- `src/tui/internal/connectors/connector.go:10` — comment, ambiguous shorthand, most likely names this file given subject matter
- agent-dotfiles / Skills: none

---

## Summary table

| File | Disposition | Real functional risk if moved |
|---|---|---|
| `ci-rules-retired.md` | historical | low — comments + golden-set fixtures only |
| `decisions/2026-09-06-knowledge-architecture-merge-exception.md` | historical | low — golden-set fixture only |
| `director-brief.md` | canonical | low — comments + golden-set fixtures only |
| `director-loop.md` | canonical | low — one CLI output string literal (`main.go`), else comments/fixtures |
| `knowledge-system.md` | canonical | **medium** — two production string literals in `main.go` feed a test-pinned grounding citation |
| `knowledge-workflow.md` | canonical, **Phase 4 owned — do not move** | n/a, excluded |
| `orientation/conventions.md` | canonical | low |
| `orientation/daemon.md` | canonical | low |
| `orientation/go-only.md` | canonical | low |
| `orientation/invariants.md` | canonical | low |
| `orientation/tui-arrival.md` | canonical | low (plus a pre-existing unrelated `AGENTS.md` naming inconsistency, noted for Phase 3) |
| `phase-plan.md` | canonical | **high** — three live `tick.KnownPhases()` call sites read this exact path at runtime; five test fixtures hardcode it too |
| `product/PRD.md` | canonical | low |
| `product/SPEC.md` | canonical | low (confirmed the indexer itself is subdir-agnostic) |
| `reviewer-value.md` | canonical | low |
| `spend-observation.md` | canonical | low (13 comment references, zero functional) |
| `tick-escalations.jsonl` | **relocate-out-of-docs** | **high** — live cron-loop write window; single subsystem (`internal/tick`) |
| `tick-log.jsonl` | **relocate-out-of-docs** | **highest** — live cron-loop write window; TWO independent subsystems (`internal/tick` + `src/tui/cmd/estate`), 8 hardcoded test literals in the TUI side |
| `tui/PRD.md` | canonical | low |
| `tui/SPEC.md` | canonical | low |

**DELETE-CANDIDATE list for Jon: none.** Zero full-text duplicates found
(checksummed all 18 `.md` files). Every file has at least one real,
current consumer (routing link, cross-reference, or golden-set fixture) —
nothing reads as orphaned or safe to drop. If Jon independently judges any
canonical/historical doc's CONTENT (not its consumer count) as no longer
worth keeping, that is a separate call this table does not make.

**research disposition: unused.** Nothing under this repo's `docs/` is
exploratory/findings-only in a way that reads as archived research rather
than operative or historical truth — `spend-observation.md` came closest
(it opens "This is a finding, not a design") but is disposed canonical
because it is actively cited by production comments as the live source of
truth for cost/spend behavior, not archived.

---

## Notes for Phase 2 (not executed here)

1. Every canonical/historical file above that has a golden-set fixture
   (`cases.json`/`natural_cases.json` `expected_identifier`) will need that
   identifier updated to match its new path if moved — mechanical, but a
   real per-file cost, and `TestEveryRepoDocsFileHasANaturalCase` may need
   checking against whatever new files (tombstones) appear under `docs/`.
2. The `AGENTS.md:115` naming inconsistency (`docs/tui-arrival.md` missing
   `orientation/`) is real and pre-existing, found incidentally during this
   consumer check — not touched here per the explicit "do not touch
   AGENTS.md" instruction; flagged for Phase 3.
3. `docs/tui/PRD.md`/`docs/tui/SPEC.md`/`docs/product/PRD.md`/
   `docs/product/SPEC.md` cross-reference each other and themselves using
   one-level-shorter shorthand paths (`docs/PRD.md`, `docs/SPEC.md`) that
   only resolve correctly assuming the reader is already inside the
   matching sibling directory — not a defect, but Phase 2 should preserve
   each pair's relative co-location if it moves either.
4. The `reference/scripts/supervisor/*.py` docstrings citing bare
   `docs/SPEC.md §15` are ambiguous between the two real `SPEC.md` files
   and possibly a third, pre-merge `agent-supervisor` doc that may no
   longer exist under that name at all — flagged, not resolved; `reference/`
   is explicitly non-canonical per `docs/orientation/go-only.md` so this is
   low priority.
5. `docs/tick-log.jsonl` and `docs/tick-escalations.jsonl`'s relocation
   cost is written out in full in their own rows above, per the director's
   explicit "give Phase 2 the full picture" instruction — summarized in the
   table only as a severity flag, not a verdict.
