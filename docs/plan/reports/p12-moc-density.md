# MOC density report — notes per hub, sorted

Generated 2026-09-07 15:15 EDT by the Director, counting `generated-links` entries per hub in `02 - MOCs`.

| notes | hub |
|---:|---|
| 136 | deploy.md |
| 127 | lane.md |
| 80 | api.md |
| 77 | auth.md |
| 72 | sweep.md |
| 71 | review.md |
| 68 | token.md |
| 64 | merge.md |
| 64 | estate.md |
| 62 | skills.md |
| 62 | git.md |
| 60 | vault.md |
| 58 | director.md |
| 56 | workflow.md |
| 56 | container.md |
| 46 | harness.md |
| 45 | knowledge.md |
| 44 | tmux.md |
| 40 | github.md |
| 40 | context.md |
| 39 | docker.md |
| 39 | branch.md |
| 37 | supervisor.md |
| 37 | memory.md |
| 37 | dispatch.md |
| 36 | cron.md |
| 35 | dotfiles.md |
| 31 | restore.md |
| 31 | gate.md |
| 30 | tui.md |
| 28 | credential.md |
| 24 | quota.md |
| 23 | pipeline.md |
| 21 | reasoning.md |
| 19 | corpus.md |
| 18 | testing.md |
| 18 | identity.md |
| 16 | codex.md |
| 16 | backup.md |
| 15 | worktree.md |
| 14 | security.md |
| 14 | cloud.md |
| 0 | Sources.md |
| 0 | README.md |
| 0 | Projects.md |
| 0 | Parameters.md |
| 0 | Notes.md |
| 0 | Meta.md |
| 0 | Inbox.md |
| 0 | Agents.md |

## What this shows, and the finding that matters

**42 topic hubs, plus 8 area hubs.** The eight zero-link rows (`Agents`,
`Inbox`, `Meta`, `Notes`, `Parameters`, `Projects`, `README`, `Sources`) are
the pre-existing area hubs — navigation, not tag-derived — so a zero there is
correct, not a failure.

**Every one of the 42 topic hubs clears the `>=8` threshold by a wide
margin.** The range is 136 (`deploy`) down to 14 (`security`, `cloud`); the
minimum is 75% above the bar. So "42/42 born" is *not* a case of marginal
hubs squeaking through — nothing marginal was born, because nothing marginal
was proposed.

The reason is worth stating, because it is circular in a way that is easy to
miss: C2's 48-value vocabulary was **derived from note frequency in the first
place**, so a value only entered the vocabulary if many notes mentioned it.
Asking afterwards which of those values have >=8 notes is close to asking
which frequent terms are frequent. The threshold did not select 42 from a
larger field — the field was already the frequent terms.

## The actual defect: the birth logic is threshold-only

Verified against `origin/main` at `8ba75ac`, read with `git show
origin/main:src/estate/internal/candidates/moc.go` rather than a working tree
— the `/tmp/estate-main` checkout was three merges stale, and reading it
would have produced a wrong answer about a function whose signature #1282
had just changed.

`moc.go` contains exactly four entry points — `walkNotes`, `MOCProposals`,
`mocLinks`, `ReviewMOC`, `RefreshMOCs` — and the only selection logic in any
of them is:

```go
if len(paths) < 8 {
```

There is **no rate-limit, no top-N, and no auto-retire** anywhere in
`internal/candidates`. So:

- **Rate-limited top-N birth: not implemented.** Every tag over threshold is
  proposed in one pass. With a frequency-derived vocabulary that means the
  whole vocabulary births at once, which is what happened.
- **Auto-retire: does not exist.** A hub whose note count later falls under
  the threshold will **not** be pruned. Nothing will notice. `RefreshMOCs`
  regenerates link blocks; it does not retire hubs.

P12 item 1 describes birth as "auto-birth rate-limited top-N, auto-retire".
Two of those three are absent from the code. The 42 hubs are not evidence the
gate works — they are evidence a threshold fired once against a field that
was already filtered. Recording this rather than reporting item 1 as done.
