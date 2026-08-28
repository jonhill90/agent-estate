---
type: Runbook
description: Executable command sequence for merging agent-tui into agent-supervisor as agent-estate, with a verification and a rollback after every step.
generated:
  at: 2026-08-23T18:10:00-04:00
---

# Runbook: `agent-tui` + `agent-supervisor` → `agent-estate`

**Status: written, not executed.** Quiesce gates running this — see
`director-merge-plan.md` and `director-drain-status.md` for the current
board state. This is written so a lane with no context on the merge
decision, the council, or any prior discussion in this loop can execute it
correctly from the doc alone. If you are that lane: read this whole file
before running the first command — later steps depend on facts established
in earlier ones.

**Source of every fact below**: `agent-supervisor#535`'s merge-impact
inventory (`docs/merge-impact-inventory-agent-estate.md`, `estate:4`,
measured against `agent-tui@8c2db69` / `agent-supervisor@35988b5`). Nothing
here is re-derived from memory. Where the inventory says a thing is
unmeasured, this runbook says so too rather than filling the gap with a
plausible-sounding command.

**Never bypass `merge-pr.sh`** (or agent-tui's own merge tool) at any point
below, including inside this migration. The independence and CI gates
apply to migration PRs exactly as they apply to everything else this
estate has ever merged.

## Layout decision this runbook builds against

```
agent-estate/
  daemon/            unmoved — agent-supervisor's existing Go daemon (module: TBD, was never canonical)
  tui/               agent-tui's cmd/ internal/ testdata/ tools/ go.mod go.sum, moved as a unit
  scripts/
    supervisor/      unmoved (agent-supervisor's scripts/supervisor/*, 105 files)
    tui/             agent-tui's scripts/* (4 files), moved here
  docs/
    supervisor/      agent-supervisor's docs/{decisions,product,runbooks,diagrams}, moved under this
    tui/             agent-tui's docs/*, moved under this
    index.md         ONE merged index, replacing both repos' separate ones
  tests/             unmoved — agent-supervisor's suite; agent-tui's *_test.go stay beside their .go files under tui/
  .github/workflows/ union — agent-supervisor's 5 (`completion-gate.yml`,
  `daemon-ci.yml`, `fixpass-evidence.yml`, `ui-evidence.yml`,
  `validate.yml` — `daemon-ci.yml` landed with `#543` after the inventory's
  own count of 4 was taken; corrected here, caught by review) + agent-tui's
  `ci.yml`, no filename collisions (re-verified against current
  `origin/main`, not the inventory's original count)
  README.md / AGENTS.md / CLAUDE.md   merged content, one file each — a decision, not a mechanical move (inventory §6)
```

Two Go modules, not one: `daemon/go.mod` and `tui/go.mod` stay independent
(different dependency sets, no evidence either needs the other's version
pins). Module paths chosen in step 4.

---

## Step 0 — preconditions, check before running anything below

- [ ] Quiesce reached: every PR in `director-merge-plan.md`'s checklist
      merged or explicitly parked (pushed branch, draft PR — never an
      uncommitted worktree). Re-check live, do not trust a cached status.
- [ ] `agent-supervisor#537` (the PR/issue metadata archive) **merged**,
      and its four cited-number resolutions re-verified against the
      merged copy (`gzip -dc docs/archive/agent-tui/agent-tui-pr-archive.jsonl.gz
      | jq -c 'select(.number==79)'`, etc. — see that doc's own README).
      This gates step 7 specifically, but confirm it here so step 7 isn't
      the first time anyone checks.
- [ ] No lane holds an active worktree under either repo. `git worktree
      list` in both, cross-checked against `tmux capture-pane` for every
      live `estate:N` pane — a worktree with no live pane behind it is
      abandoned, not active; one with a live pane is not safe to touch.
- [ ] Step 0.5 (below) landed and is green — this repo's daemon has never
      had a CI gate; add one before, not after, the one change most likely
      to break an unguarded compile silently.

If any box is unchecked, stop. Do not proceed on "probably fine."

---

## Step 0.5 — gate the daemon build, before anything else moves

**Decided separately** (`agent-supervisor#542`, filed alongside this
runbook): `daemon/` has never had a `go build`/`go vet`/`go test` gate in
CI, verified by reading all four workflow files and confirmed zero-cost so
far (the daemon has built clean at every one of the 7 commits that have
ever touched it, checked directly via `git archive` + `go build`, not
inferred from an absence of "fix the daemon build" commits). Adding the
gate now, before step 1's rename and step 2/4's file moves and module-path
rewrite, gives a known-good green baseline — without one, a post-migration
daemon compile break can't be told apart from a pre-existing gap.

**`agent-supervisor#543` has merged (`2026-08-24T00:57:53Z`) — confirm it's
green, don't land it.** ("Land" was accurate when this section was first
written; it's stale now and reads as a pending action an operator might
try to redo. Caught by review at this exact head.) Do not write a
second copy of this workflow. An earlier draft of this section embedded
its own inline YAML here; `estate:5`'s review of this runbook caught that
`#543` had since landed as the real implementation, with two deliberate
improvements a hand-copied version would silently lose:

- `#543` deliberately omits a `push: { branches: [main] }` filter —
  `agent-supervisor#407`'s own measured finding is that scoping `push` to
  `main` only leaves a `CONFLICTING`-mergeable PR's own branch with zero
  check runs until it happens to land, a real blackout window `#543`
  avoids by triggering on `pull_request` unfiltered.
- `#543` sets `cache-dependency-path: daemon/go.sum` after a real run
  showed the default Go module cache key misses for this repo's layout
  (confirmed live on `#543`'s own first uncached run: "Restore cache
  failed... go.sum").

Mutation-check it per `#543`'s own review before trusting it, the same
standard this runbook holds every other step to: on a scratch branch,
introduce a deliberate compile error in `daemon/`, confirm the check goes
red; separately, a failing test with no compile error, confirm `go test`
alone blocks; revert both, confirm green. `#543`'s own review already did
this and it held — re-run it here only if time has passed since.

**Rollback:** revert `#543`'s single workflow-file commit.

---

## Step 1 — rename `agent-supervisor` → `agent-estate`

### 1a. GitHub rename

```
gh repo rename agent-estate --repo jonhill90/agent-supervisor
```

**Verify:**
```
gh repo view jonhill90/agent-estate --json name,visibility,isArchived
# expect: name=agent-estate, visibility=PUBLIC, isArchived=false

git ls-remote https://github.com/jonhill90/agent-supervisor.git
# GitHub redirects a renamed repo's old URL -- expect this to still list
# refs, not 404. If it 404s, the rename did not register a redirect;
# stop and investigate before continuing.
```

**Rollback:** `gh repo rename agent-supervisor --repo jonhill90/agent-estate`
— reversible immediately after, GitHub preserves the redirect either way.

### 1b. Local canonical checkout

The canonical checkout's directory name and every worktree's back-reference
need to move together, or `git worktree` metadata breaks (each worktree's
`.git` file points at an absolute path inside the main checkout's
`.git/worktrees/<name>/`, and the main checkout's own `.git/worktrees/*/gitdir`
points back).

```
mv /Users/jon/source/repos/Personal/agent-supervisor \
   /Users/jon/source/repos/Personal/agent-estate
cd /Users/jon/source/repos/Personal/agent-estate
git remote set-url origin https://github.com/jonhill90/agent-estate.git
git worktree repair
# THEN, separately -- see the measured finding below, this bare call alone
# does not fix .worktrees/-internal worktrees:
git worktree repair .worktrees/*
```

**Verify:**
```
git worktree list
# every entry should resolve (no "prunable" or path-not-found lines)

git -C /Users/jon/source/repos/Personal/agent-estate/.worktrees/<any-live-one> status
# confirm at least one linked worktree still functions post-repair
```

**Measured 2026-08-28 (agent-supervisor#682), replacing the prior "could not
measure"**: bare `git worktree repair` (no arguments -- the command above,
as this runbook wrote it until now) is **not sufficient** at this repo's
real scale. Built a throwaway origin+checkout mirroring the live shape (21
internal `.worktrees/`-style + 107 external absolute-back-pointer
worktrees, matching agent-supervisor's real registered count; repeated at
agent-tui's 4+28 shape), renamed the checkout directory exactly as this
step does, and ran the bare command:

```
before=129 after=129 prunable=21 not_found=21 status_fail=5 repair_rc=0
```

Every one of the 21 internal worktrees came back `prunable`, with `git
status` failing `fatal: cannot change to '.../checkout/.worktrees/wt-N':
No such file or directory` -- `git worktree list` was still printing the
PRE-rename path. This is not a bug in `git worktree repair`, it is
documented, narrower behavior than the bare invocation implies: per `git
help worktree`, a moved MAIN worktree only reconnects LINKED worktrees
automatically when neither side needs its own path corrected; a linked
worktree that itself moved (which every `.worktrees/*` entry did here, as
a side effect of the parent directory rename) needs its **new path passed
explicitly** — `git worktree repair <path>...` — or `repair` run from
inside that worktree. Re-running with `.worktrees/*` passed explicitly, at
the same 129-worktree scale:

```
before=129 after=129 prunable=0 not_found=0 status_fail=0 repair_rc=0
```

Zero prunable, zero not-found, `git status` verified correct on a sample of
both internal and external worktrees. The 107 EXTERNAL worktrees needed no
extra argument in either run — they didn't move (only the main checkout
did), so the bare call's automatic linked-worktree reconnection already
covers them; only the internal ones require the explicit-path form. This is
why the command above is now two calls, not one — the second is the actual
fix this measurement found, not a defensive extra step.

**Rollback:** `mv` the directory back, `git remote set-url origin
https://github.com/jonhill90/agent-supervisor.git`, `git worktree repair`
again. Safe as long as 1a's rollback hasn't also happened (don't reverse
only one half).

---

## Step 2 — merge `agent-tui`'s history in, under `tui/`

Do this on a branch, in the renamed checkout, never on `main` directly.

### 2a. Rewrite `agent-tui`'s history into a `tui/` subdirectory, in a scratch clone

```
git clone --no-local https://github.com/jonhill90/agent-tui.git /tmp/agent-tui-rewrite
cd /tmp/agent-tui-rewrite
git filter-repo --to-subdirectory-filter tui
```

This produces `.git/filter-repo/commit-map` — a two-column old-SHA/new-SHA
mapping for every rewritten commit (paths change, so tree hashes and
therefore commit hashes change; the original 89 SHAs will not exist
verbatim in the merged repo — this is expected, not history loss, and the
commit-map is exactly the tool for proving that).

**Prefer this over `git merge --allow-unrelated-histories` on the raw,
un-rewritten history**: without the subdirectory prefix, every one of the
top-level collisions (`README.md`, `AGENTS.md`, `CLAUDE.md`, `docs/`,
`scripts/`, `.github/`) would conflict as "added by both" on the very first
merge attempt, with no clean 3-way resolution (unrelated histories share no
common ancestor for git to diff against). Prefixing first means only the
files meant to live under `tui/` collide with nothing.

### 2b. Merge the rewritten history into `agent-estate`

```
cd /Users/jon/source/repos/Personal/agent-estate
git checkout -b migrate/agent-tui-merge
git remote add tui-rewritten /tmp/agent-tui-rewrite
git fetch tui-rewritten main
git merge --allow-unrelated-histories tui-rewritten/main \
  -m "merge agent-tui's history under tui/ (89 commits, --allow-unrelated-histories)"
```

**Verify — prove history survived by resolving a known old SHA, not by
trusting the merge command's exit code:**

```
# Pick a known old agent-tui SHA -- e.g. 557255a, PR #135's rename commit
grep "^557255a" /tmp/agent-tui-rewrite/.git/filter-repo/commit-map
# -> 557255a... <new-sha>

git log --format=%H | grep <new-sha>
# expect: found. Use --format=%H (full 40-char SHA), NOT --oneline --
# --oneline prints abbreviated 7-char hashes, and grepping a full SHA from
# the commit-map against that output can never match regardless of whether
# the merge actually worked (estate:2's review of this runbook, #541,
# caught this live: the commit was genuinely present -- `git log --oneline
# | grep <short-sha>` finds it immediately -- but the literal command as
# first written here would report "not found" for a merge that fully
# succeeded, which is exactly the false-negative this step exists to
# prevent).

git log --format=%H%n%s | grep -A1 <new-sha>
# same lookup, paired with the commit message on the next line, so you
# also see "rename(keelson): the product is the Estate..." without a
# separate command

git show <new-sha>:tui/go.mod
# expect: module github.com/jonhill90/agent-tui (not yet rewritten -- that's step 4)

diff <(git -C /tmp/agent-tui-rewrite show <new-sha>:tui/go.mod) \
     <(git show <new-sha>:tui/go.mod)
# expect: no output -- content identical between the scratch rewrite and
# what actually landed in agent-estate
```

Repeat the SHA check for at least one more commit from a different point in
the 89-commit range (e.g. the very first `agent-tui` commit) — one sample
proves the tip merged, not that every commit's content survived.

**Rollback:** everything above happened on `migrate/agent-tui-merge`, not
`main`. `git checkout main && git branch -D migrate/agent-tui-merge`,
remove the scratch clone and the `tui-rewritten` remote. Nothing pushed to
`origin` yet at this point.

### 2c. Resolve the top-level content collisions

Per the inventory (§6): **not mechanical, a content decision per file.**
On `migrate/agent-tui-merge`, still uncommitted (or in a follow-up commit):

```
git mv tui/scripts scripts/tui
git mv tui/docs docs/tui
```

`README.md`, `AGENTS.md`, `CLAUDE.md`: merge by hand into the existing
top-level files as clearly-labeled sections (`## The daemon`, `## The
TUI`) — do not concatenate blindly; both currently open with framing
("before you ask Jon anything" / naming-history) that should exist once,
not twice, in the merged file.

`docs/index.md`: write one new index at the root, grouped by area
(`supervisor/`, `tui/`), replacing both repos' separate indexes
(`agent-supervisor#533` added `agent-supervisor`'s own `docs/index.md` —
corrected here from an earlier misattribution to `#537`, caught by
`estate:2`'s review of this runbook: `#537` is the PR/issue metadata
archive, `docs/archive/agent-tui/...`, and adds no `index.md` anywhere;
`agent-tui`'s own `docs/index.md` — `agent-tui#136` — becomes
`docs/tui/index.md`'s content, folded into the new root index rather than
kept as a second competing map).

**Verify:** `find . -maxdepth 2 -iname "index.md"` returns exactly one
hit (root). `git status` shows no remaining path under bare `tui/docs` or
`tui/scripts`.

**Rollback:** still on the migration branch, not pushed — `git reset
--hard` to the state after 2b, redo.

---

## Step 3 — transfer `agent-tui`'s open issues

**Do this after step 2 is verified, not before** — an issue transferred
before the merge lands still needs the merge to actually happen for its
cross-references to resolve.

```
gh issue list --repo jonhill90/agent-tui --state open --json number,title \
  > /tmp/agent-tui-open-issues.json

jq -r '.[].number' /tmp/agent-tui-open-issues.json | while read -r n; do
  new_url=$(gh issue transfer "$n" jonhill90/agent-estate --repo jonhill90/agent-tui 2>&1)
  echo "$n -> $new_url" | tee -a /tmp/issue-transfer-map.txt
done
```

**`agent-tui#139` (the ccusage stampede issue, live work as of this
writing) must be in this list and must transfer successfully — check its
line in the map explicitly, not just the aggregate count.**

**Verify — list them in the destination, don't trust the transfer command's
own success message:**

```
gh issue list --repo jonhill90/agent-estate --state open --json number,title \
  | jq 'length'
# expect: (agent-estate's own pre-existing open-issue count) + (count of
# /tmp/agent-tui-open-issues.json)

# spot-check the map itself, not just the count:
grep "^139 " /tmp/issue-transfer-map.txt
gh issue view <the-new-number-from-the-map-line-above> --repo jonhill90/agent-estate \
  --json title -q .title
# expect: "Cost pane stampedes ccusage -- 12 concurrent calls, ~2.7 load/core, no in-flight guard"
```

**Rollback: issue transfer is not cleanly reversible.** GitHub assigns the
issue a NEW number in the destination; transferring it back creates a
THIRD number, not the original. If a transfer is wrong, transfer back and
document the full number chain (old → wrong destination number → corrected
number) in the issue body itself — do not pretend the original number
comes back.

---

## Step 4 — the rename sweep

Classification is the inventory's own (§ "Addendum classification"). **Only
touch (a) and (b). Never touch (c).**

### 4a. Class (a) — must change before this migration is considered done

| # | What | Fix |
|---|---|---|
| 1 | `ledger.lanes.repo` rows | Step 5, below — its own step, not folded in here |
| 2 | `agent-tui/.github/workflows/ci.yml`'s two-checkout shape | Step 6, below |
| 3 | 4 launchd plists (`director-loop`, `quota-watch`, `supervisor-heartbeat`, `weekly-watch`) | `sed -i '' 's#/agent-supervisor/#/agent-estate/#' ~/Library/LaunchAgents/com.jonhill.{director-loop,quota-watch,supervisor-heartbeat,weekly-watch}.plist`, then `launchctl unload` + `launchctl load` each |
| 4 | `~/.local/state/estate-loop/{check.sh,tick-scan.sh,status.sh}` | Un-versioned, outside both repos — edit the paths and repo-name literals directly, no PR. `check.sh:115`'s `cd .../agent-tui \|\| exit 0` becomes moot once `agent-tui` is deleted (step 7) — repoint at nothing, or delete the check, rather than pointing it at a directory that will stop existing |
| 5 | `scripts/supervisor/cli.py:83-88` `DEFAULT_REPOSITORIES` | One edit: `agent-supervisor` entry's `path` and `github` fields become the `agent-estate` equivalents. This is the highest-leverage single edit in this whole sweep — everything resolving `--repo <name>` through `cli.py` inherits the fix |
| 6 | `closed-report.sh:50`, `watchdog.sh:74`, `digest.sh:42`, `refresh_brief_resume.py:56`, `acceptance.sh:69`, `contest-stop.sh:175` | Same rename, one literal each, one PR |
| 11 | `scripts/supervisor/verdict.py`, `scripts/supervisor/ci_gate.py` (agent-supervisor); `internal/prverdict`, `internal/mergepr` (agent-tui) | **Found missing from this list by review, added here rather than left absent.** These query `ledger.pr_verdicts.repo`/`pr_authorship.repo` by exact repo-name match. 4c's own historical rows keep the OLD name (`agent-supervisor`/`agent-tui`) permanently — so this code must match on **both** the old and new (`agent-estate`) names, not just switch to the new one, or every pre-migration row becomes silently unmatchable the moment this code stops recognizing the name that's actually stored |

**Verify each:** for the launchd jobs, `launchctl list | grep com.jonhill`
shows each loaded with no error, and `log show --predicate 'process ==
"bash"' --last 5m` (after a manual `launchctl kickstart`) shows the job ran
against the new path. For the scripts, `grep -rln "Personal/agent-supervisor"
scripts/supervisor/*.sh scripts/supervisor/*.py` returns zero after the
edits (this doubles as the completeness check for class (a)/#5/#6 together).

**Rollback:** each is a small, independent, revertible commit/edit — revert
the specific commit or restore the specific plist/script from git history
(the un-versioned `estate-loop` scripts have no git history to restore
from; keep a `.bak` copy before editing those three specifically).

### 4b. Class (b) — must change, can follow the migration itself

| # | What | Note |
|---|---|---|
| 7 | `agent-dotfiles/settings/mcp/servers.json:22` | Dormant today (not in the live `~/.claude.json`) — fix before the next `scripts/sync.py` run, not necessarily before this migration lands |
| 8 | `tui/cmd/estate/{main.go,supervisor.go}`'s `AGENT_SUPERVISOR_REPO`/`-supervisor-repo` resolution | Post-merge this flag is resolving a SIBLING checkout that no longer needs to be sibling — `scripts/supervisor/` now lives in the same repo, under the merged tree's own `scripts/supervisor/`. This is real follow-on engineering (repoint the resolver at the merged repo's own root, or delete the flag entirely and use a repo-relative path), not part of this inventory's mechanical scope — file it as a fast-follow issue, don't silently skip it |
| 9 | `scripts/supervisor/session-defaults.sh:8`, `scripts/supervisor/quota-watch.sh:71` | Tmux target/session-name defaults — low blast radius (only fire with no repo context, or if the tmux session is separately renamed) |
| 10 | `scripts/supervisor/dispatch.sh:375` | Cosmetic — an error message's own text names `agent-supervisor#17`; harmless to leave, cheap to fix in the same pass as 4a/#6 |

### 4c. Class (c) — do not touch, ever

- `ledger.pr_verdicts.repo` (1 row), `ledger.pr_authorship.repo` (5 rows),
  `ledger.source_tasks.source_url` (706 `agent-supervisor` + 115
  `agent-tui` URLs), `ledger.items.body` (31 rows), `ledger.prompts.text_raw`/
  `text_clean` (447 rows) — historical record of what was true when each
  row was written. Rewriting any of these to say `agent-estate` **is** the
  failure mode `CLAUDE.LOCAL.md`'s "never rewrite history to match a
  rename" rule exists to prevent.
- `~/.local/state/estate-loop/*.md` (~130 dispatch briefs, this file
  included) — one-shot, read-once records. Leave the old repo name in
  every historical one; only new briefs written after this migration
  should use `agent-estate`.
- Any code comment or doc prose that is itself a citation of history (e.g.
  `agent-tui`'s own etymology note on why the binary used to be called
  `keelson`) — same reasoning as the naming-history precedent PR #135 set:
  extend the record, don't erase it.

**Verify class (c) was actually left alone:** after 4a/4b land, diff the
ledger's row counts for the five tables above against their pre-migration
counts (captured in step 0) — every count must be identical. A changed
count here means something class-(c) got touched; stop and investigate
before continuing to step 5.

---

## Step 5 — `ledger.lanes.repo`, the top-ranked silent break

**Why this is ranked #1** (inventory's own framing): a stale path here
doesn't error. Dispatch just can't find the lane's own repo, or silently
finds the wrong thing, with no message pointing at why.

```
sqlite3 ~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3 <<'SQL'
SELECT lane, repo FROM lanes WHERE repo = '/Users/jon/source/repos/Personal/agent-supervisor';
SQL
# capture this output BEFORE the update, as the rollback reference

sqlite3 ~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3 <<'SQL'
UPDATE lanes SET repo = '/Users/jon/source/repos/Personal/agent-estate'
WHERE repo = '/Users/jon/source/repos/Personal/agent-supervisor';
SQL
```

Run this **only after** step 1b's directory rename has actually happened —
updating the ledger to point at a path that doesn't exist yet is the exact
silent-break failure mode this step exists to close.

**Verify:**
```
sqlite3 ~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3 \
  "SELECT lane, repo FROM lanes WHERE repo LIKE '%agent-supervisor%'"
# expect: zero rows for the canonical (non-ephemeral) path. Ephemeral
# /tmp and /private/var/folders/.../T/ worktree paths from OLD, already-
# completed tasks may still legitimately say "agent-supervisor" in their
# path string incidentally (e.g. a worktree name like
# "ad-478-review-493-78342") -- those are fine, they're historical and
# regenerate fresh per dispatch; only the canonical, currently-active,
# non-ephemeral row matters here (per §7a of the inventory).

bash scripts/supervisor/lanes.sh "" estate
# confirm every live estate:N lane still classifies correctly post-update
# -- this is the actual functional proof, not just a row-count check
```

**Rollback:** re-run the `UPDATE` with the WHERE/SET clauses swapped back,
using the pre-update capture above as the source of truth for exactly
which lanes to revert (don't blanket-revert every `agent-estate` row —
some may be legitimately new by the time you'd roll back).

---

## Step 6 — `agent-tui`'s CI two-checkout shape

Ranked #2 (silent, and worse than silent: stays *green* while validating a
frozen pre-merge snapshot once `agent-supervisor` no longer exists as a
separate clonable repo under that name).

Current shape (`.github/workflows/ci.yml:11-31`, now `.github/workflows/ci.yml`
inside the merged repo, or wherever step 2c's `.github/workflows/` union
landed it):

```yaml
- uses: actions/checkout@v4
  with: { path: agent-tui }
- uses: actions/checkout@v4
  with: { repository: jonhill90/agent-supervisor, path: agent-supervisor }
- run: go test ./...
  working-directory: agent-tui
  env: { AGENT_SUPERVISOR_REPO: ${{ github.workspace }}/agent-supervisor }
```

New shape — one checkout (the merged repo checks itself out once, like
every other job in this repo's other workflows already does), and
`internal/lane/states_lanessh_test.go`'s cross-check reads `lanes.sh` from
a same-checkout relative path instead of a second clone:

```yaml
- uses: actions/checkout@v4
- run: go test ./tui/...
  env: { AGENT_SUPERVISOR_REPO: ${{ github.workspace }} }
```

**Verify:** push this change on a branch, confirm the CI run is green, and
specifically confirm `TestAllStatesCoversLanesShStates` did not skip
(the test's own skip message is distinctive: `"AGENT_SUPERVISOR_REPO not
set"` — grep the CI log for it; its ABSENCE from the log is the proof the
cross-check actually ran rather than silently passing-by-skipping).

**Rollback:** revert the single workflow-file commit.

---

## Step 7 — delete `agent-tui`, LAST, gated

**Do not run this until every box below is checked. Deletion is
irreversible in every way that matters — commits survive via step 2's
history merge, issues survive via step 3's transfer, but nothing after
this point can recover a PR review thread that wasn't captured first.**

- [ ] Step 2 verified (history present, sampled at 2+ commits, content
      diffed identical).
- [ ] Step 3 verified (every open issue, especially `#139`, resolves in
      `agent-estate` under its new number).
- [ ] `agent-supervisor#537`'s PR/issue archive merged, AND re-verified
      against its merged copy — not the pre-merge snapshot checked in
      step 0. Re-run its own README's verification commands one more time
      here, on whatever `agent-tui` state exists right at deletion time
      (anything that landed on `agent-tui` between step 0 and now needs a
      fresh capture — the archive's own README says this explicitly).
- [ ] `go build ./... && go vet ./... && go test ./...` clean inside
      `agent-estate/tui/`, at the merged tree's actual head, not the
      pre-merge branch.
- [ ] Step 6 verified (CI green under the merged shape).
- [ ] No open PR remains in `agent-tui` — check `gh pr list --repo
      jonhill90/agent-tui --state open`; anything found here is lost the
      moment the repo is deleted (a PR's branch and diff do not survive
      deletion the way a merged commit does). Land, close, or manually
      preserve (patch file, committed to `agent-estate`) each one first.

```
gh repo delete jonhill90/agent-tui --confirm
```

**Verify:**
```
gh repo view jonhill90/agent-tui
# expect: 404 / "could not resolve to a Repository"

git clone https://github.com/jonhill90/agent-tui.git /tmp/should-fail
# expect: fails -- repo genuinely gone, not just hidden
```

**Rollback: none.** GitHub does not offer self-service undelete for a
repository past its own brief post-delete grace window (if any is offered
at delete time, it is a GitHub-support-mediated path, not a command this
runbook can specify). This is why every prior step in this runbook exists
— by the time step 7 runs, there should be nothing left in `agent-tui`
that step 7 itself is the only copy of.
