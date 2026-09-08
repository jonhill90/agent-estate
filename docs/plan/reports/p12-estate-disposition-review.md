Verdict: APPROVE
Review-Lane: agent-estate:3

# Cross-lane review — P12 Phase 1, agent-estate docs/ disposition table

Reviewing `run/p12-estate-disposition.md` (lane-b, agent-estate:2) against
`run/p12-execution-plan.md`'s Phase 1 gate: not "are these classifications
reasonable" but "can Phase 2 execute this table mechanically, with no
re-deciding, without being wedged." Fresh clone of `jonhill90/agent-estate`
at `origin/main`, confirmed `8ba75ac4e626e6872c69b333355fb7f1c16f0465` —
matches the table's own cited SHA exactly.

## (1) Completeness — measured, not assumed

```
$ find docs -type f | sort   # fresh clone, 8ba75ac
```
→ 20 files. Table has 20 `## docs/...` row headers.
`diff` between the real file list and the extracted row list (both
sorted): **empty — exact match.** No row for a file that doesn't exist,
no file without a row.

## (2) Consumer-check spot-checks — 8 rows, every disposition class present

Re-ran the greps myself, did not trust the pasted output:

- **`ci-rules-retired.md`** (historical): `AGENTS.md:105` link — confirmed exact text. `goldenset/cases.json:112-113` and `natural_cases.json:186-188` — confirmed, three real `expected_identifier`/`rationale`/`note` fixture lines. `docs/tick-log.jsonl:10` — confirmed the exact artifact-field JSON line.
- **`knowledge-system.md`** (canonical, flagged highest functional risk): confirmed both `main.go` string literals ("...or see `docs/knowledge-system.md`'s...", "...see `docs/knowledge-system.md` for the full scoping rules") are real, live, in the grounding-text-building code, and `knowledge_grounding_scoping_test.go:28`'s `strings.Contains(got, "docs/knowledge-system.md")` assertion is real. This row's "medium" risk and the exact remediation shape (update both literals + this test) checks out.
- **`phase-plan.md`** (canonical, flagged high risk): confirmed all three `tick.KnownPhases("docs/phase-plan.md")` call sites at the exact cited line numbers (2241, 2389, 2471) in `main.go`. This is a real runtime file-open, not a comment — the "high" risk label is earned.
- **`tick-log.jsonl`** (relocate-out-of-docs, flagged highest risk): confirmed `internal/tick/tick.go:32`'s `DefaultPath` constant, confirmed `src/tui/cmd/estate/main.go:87`'s independent `flag.String` default — genuinely two separate hardcoded subsystems, not one, exactly as claimed.
- **`tick-escalations.jsonl`** (relocate-out-of-docs): confirmed `internal/tick/tick.go:48`'s `DefaultEscalationPath` constant at the cited location.
- **`orientation/conventions.md`**'s false-positive claim: confirmed the five `src/tui/...` hits the table excludes are genuinely about the vault's `memory-conventions.md`, a different file — correctly excluded, not a missed consumer.
- **`spend-observation.md`**: confirmed exact counts, 6 hits in `harness.go` and 5 in `harness_test.go`.
- **"None found" claims**, spot-checked across all three other repos: `go-only.md` and `reviewer-value.md` return zero hits in the vault (confirmed); `knowledge-system.md` and `phase-plan.md` return zero hits in fresh `agent-dotfiles` and `Skills` clones (confirmed). No missed consumer surfaced in any "none" row I checked.

**One minor, non-blocking discrepancy found:** the `tick-log.jsonl` row's `tickpath_test.go` citation says "8 hits" but lists 10 line numbers (`:21,25,32,52,75,80,103,136,204,231`), and a broader grep for the bare substring `tick-log.jsonl` (not the fully-quoted string) turns up two more real hits the row doesn't list at all (comments at lines 180 and 190). This doesn't change the disposition, doesn't understate the risk (the row already carries the highest severity label and the most detailed remediation cost in the table), and doesn't leave Phase 2 unaware of the dependency — it's a transcription/count slip on the single most heavily-cited row in the table, not a missed consumer class. Noted for the record; not blocking.

## (3) Live-state rows — the one the Director named specifically

`tick-log.jsonl` and `tick-escalations.jsonl` rows both state, in full, not
as a one-word disposition: the exact constant (`DefaultPath` /
`DefaultEscalationPath`), the resolution rule (env override else default,
against process cwd), the pinning test's actual scope (confirmed by
reading `tick_check_discloses_path_test.go` directly — the table's own
claim that it doesn't hardcode the literal path as an assertion, only as
a doc-comment description, checks out), the live-cron-loop-write-window
hazard stated as a real coordination risk (not hypothetical), and for
`tick-log.jsonl` specifically, the second independent TUI-side subsystem
plus its own 8-10 hardcoded test literals. Confirmed accurate and
substantially complete (see the one gap noted in (2) above). This is not
thin — it is the most thoroughly worked section of the whole table, and
correctly flagged as "real, multi-subsystem work... budget it as such"
rather than folded into a one-line mechanical move.

## (4) DELETE-CANDIDATE — verified, and checked the reverse error too

Table lists zero DELETE-CANDIDATEs, on the stated basis of a full-text
checksum sweep. Independently re-ran: `find docs -name "*.md" | xargs
sha256sum | sort | uniq -d` on the fresh clone → empty, 18 files, zero
duplicates — matches exactly. Checked the reverse failure mode (something
that should be DELETE-CANDIDATE quietly filed as historical to dodge the
conversation): both `historical`-disposed files
(`ci-rules-retired.md`, the merge-exception decision doc) have real,
current, non-trivial consumers (routing links, golden-set fixtures) —
neither reads as a disguised deletable. No reverse error found.

## (5) Zero moves happened

`git status --short` in lane-b's actual working checkout
(`agent-estate-lanes/lane-b`) is clean — no renames, no deletions,
nothing staged. No open PR in `jonhill90/agent-estate` touches `docs/`
(checked `gh pr list`, three open PRs, none docs-related). Matches the
table's own "zero file moves made" claim.

## (6) AGENTS.md / CLAUDE.md / knowledge-workflow.md untouched

`git status --short` clean confirms nothing was edited anywhere in the
checkout, AGENTS.md/CLAUDE.md included. The table's own
`knowledge-workflow.md` row explicitly marks it "OWNED BY PHASE 4,
excluded from this phase's move execution regardless of table
disposition" and states Phase 2 must not move or edit it — correct
per the execution plan's phase-4 ownership. Table proposes no edit to
`AGENTS.md` anywhere (the one inconsistency it found there — the
`docs/tui-arrival.md` missing-`orientation/` naming slip — is explicitly
flagged as "not fixed here... flagged for Phase 3," not acted on).

## Conclusion

Complete (20/20, exact diff), accurate on every spot-checked row bar one
minor citation-count slip that doesn't change any disposition or hide any
real dependency, thorough on the two live-state rows the Director named
specifically, correct on DELETE-CANDIDATE (none, verified two ways),
zero moves made, and correctly out of scope on AGENTS.md/CLAUDE.md/
knowledge-workflow.md. This table is executable by Phase 2 without
re-deciding anything.

Verdict: APPROVE
Review-Lane: agent-estate:3
