# The daemon — conventions

*Relocated verbatim from the repo root `AGENTS.md` by the progressive-disclosure split (only heading levels and relative link paths were adjusted for the new location). `AGENTS.md` is the index that routes here; this file is the detail.*

## Conventions

- Your branch is `dispatch/<id>`, created by the estate, and you push it
  as-is. Never commit to `main`, and never open a PR from a hand-named branch
  — the merge gate joins authorship through that head ref and refuses
  anything else structurally (invariant 9).
- One independent review per PR, by someone who did not write it — including
  fixup commits. A review turn is dispatched with `estate dispatch review`,
  which records `role=reviewer` at dispatch time, so the gate never has to
  infer the role later from what a lane or a PR comment claims about itself.
- **A reviewer's verdict must exist in TWO places, not one: the PR comment
  AND the reviewing turn's own final returned text — identical `Verdict:`
  lines in both.** The turn's own returned text is what the dispatch process
  writes as that lane's ledger `Result`, and `internal/gate` cross-checks the
  PR comment against it as a second, independent source before allowing a
  merge (`resolveResultVerdict` in `gate.go`/`verdict.go`) — this is not
  redundant with the PR comment and must not be dropped as such. For a review
  turn: after posting the PR comment, make the verdict block
  (`Verdict:`/`Review-Lane:`/`Reviewed-SHA:`) the last thing your own turn
  returns too, not a prose summary of what you found. For the incident that
  motivated this (PR #1219/agent-estate#1220), the forgery this closes
  (`gate_test.go`'s `TestBypass_ForgedVerdictCommentImpersonatesReviewer`,
  agent-estate#934), and the cost/benefit case for review generally, see
  [`docs/canonical/reviewer-value.md`](../canonical/reviewer-value.md).
- This is checked at merge, not just at dispatch — but read the command's name
  as a question, not an action. **`estate merge <repo> <pr> <reviewer-lane>`
  evaluates and exits; it does not merge anything.** It decides whether the PR
  may merge — open, every required check green at the live head SHA, author and
  reviewer different dispatches, and an independent parsable APPROVE posted at
  that same head — then prints its verdict. Exit 0 prints `may merge: …` and
  **you still have to run `gh pr merge` yourself**; exit 1 prints each refusal
  reason to stderr. `internal/gate` shells out only to `gh pr view`; it has no
  `gh pr merge` call anywhere. That the name promises an action it never
  performs is **agent-estate#980, open** — so the gate is advisory in the
  literal sense that skipping the evaluation, not running `gh pr merge`, is
  what bypasses it.
- The gate refuses any head ref that is not a `dispatch/<id>` branch:
  authorship here is established structurally, and it cannot be established at
  all for an operator-authored branch. The refusal is correct — and it means
  **an operator-authored PR has no gated merge path today.** (`agent-estate#940`
  is closed; it is the change that built this join, and the refusal message
  names it.)
- One fix pass. If a PR fails a second review, close it and file what
  remains. A fix pass continues the PR's own branch (`estate dispatch fix`),
  never a fresh one.
- Cheaper model tiers for workers and reviewers; reserve the expensive tier for
  judgement.
- Anything touching tmux behaviour runs against an isolated socket or on a
  throwaway host — never the machine you are working on (invariant 4).
- **Credential store — read-only, no exceptions.** Never write, reset, or
  probe the macOS Keychain; a failed read is a report, not a repair. See
  `agent-dotfiles/AGENTS.md` for the canonical rule and incident rationale
  (agent-estate#665).
- **Nothing hand-authored or pane-written merges**, until the per-instance
  re-dispatch cost starts to dominate — at which point revisit. A change must
  go through a dispatched turn with a ledger-resolvable author; the merge
  gate is what makes this structural rather than advisory.
- A UI PR needs a captured frame, not a description, as evidence. **This is a
  convention now, not a gate** — `.github/workflows/ui-evidence.yml` was
  retired on 2026-09-02 (see
  [`docs/historical/ci-rules-retired.md`](../historical/ci-rules-retired.md)) and nothing fails a
  PR that omits the frame. The capture helper is `src/tui/cmd/vhscapture`,
  run from `src/tui`; `src/tui/testdata/vhs/README.md` explains its colour
  floor and what is and is not measured. Local only — it is not wired into
  CI. Reimplementing the gate in Go is open work.

---
*The claims in this section were checked against this branch's own tree as
rebased onto `45326b6` (2026-09-05) — every path, command and count above was
re-run, and what could not be found is named as absent rather than described.
The reviewer-verdict two-place requirement above was derived directly from
`src/estate/internal/gate/gate.go`'s refusal paths and `verdict.go`'s
`resolveResultVerdict` doc comment, not restated from a summary of them
(agent-estate#1220).
Re-check before relying on any of it: `src/estate/agents_md_test.go` is the only
automated check on this file, and it validates **subcommand names only** — it
would pass a section that described every one of them doing the wrong thing.
A review caught exactly that here: this section once said `estate merge`
merges. It does not merge; it decides. Read a verb against the code, never
against the command's name.*
