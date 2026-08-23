# Merge impact inventory — agent-tui + agent-supervisor → agent-estate

Author-Lane: estate:4

**Purpose.** The merge of `jonhill90/agent-tui` and `jonhill90/agent-supervisor`
into one repo (`agent-estate`) is decided; this document does not revisit that.
It is the pre-flight list of everything the merge will break, so the merge
finds those breakages here, in review, rather than one at a time in
production. Every entry below carries a file and line (or an equivalent exact
locator — a table row, a plist key, a SQL query) an executor can act on
directly.

**Method note, per the brief's own instrument-check rule.** Every "N files"
or "zero" figure below was produced by a grep/query that was first run
against a string I already knew was present in that same tree, to prove the
search mechanism itself was working before trusting a low or zero count. Each
section states its own positive control.

**Repos and worktrees this was measured against** (read-only, both, per the
brief — nothing in either working tree was created, stashed, committed, or
cleaned by this pass):

- `agent-tui` — fresh worktree off `origin/main`, `8c2db69`
  ("docs: the tape-build guard doc — closes #136's own Q3 could-not-measure
  (#138)").
- `agent-supervisor` — fresh worktree off `origin/main`, `35988b5`
  ("fix(gc): scope worktree.sh gc to .worktrees/, mutation-prove age/liveness
  through the sweep (#530)").
- The ledger at `~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3`
  (live, queried read-only).
- `~/.claude/`, `~/.local/state/`, `~/Library/LaunchAgents/`, `crontab -l`,
  `~/source/repos/Personal/Skills`, `~/source/repos/Personal/agent-dotfiles`
  — swept by a dispatched read-only agent for the ADDENDUM's external-pins
  ask (§7); its positive controls are stated inline in that section.

---

## Ranked — what breaks silently first

This is the order to fix in, not the order the sections below are numbered.
A "loud" break fails a build, a test, or a `gh` call with a nonzero exit; a
"silent" one succeeds, or looks like it did, while doing the wrong thing or
nothing at all.

| # | What | Why it's silent | Where |
|---|---|---|---|
| 1 | `lanes.repo` rows for the two live checkouts (`agent-dotfiles`, `agent-supervisor`) | A stale path here doesn't error — dispatch just can't find the lane's own repo, or finds the wrong one, with no message the brief didn't already name as the worst case | ledger `lanes` table, §7 |
| 2 | `agent-tui/.github/workflows/ci.yml`'s two-checkout pattern | If left as-is after merge, CI checks out `jonhill90/agent-supervisor` as a **second, separate clone** from GitHub — the moment that repo is archived/renamed post-merge, this either 404s (loud, good) or, worse, silently checks out a frozen pre-merge snapshot that no longer matches the merged tree, so `AGENT_SUPERVISOR_REPO` points at code nobody is looking at while CI stays green | `.github/workflows/ci.yml:11-19`, §3 |
| 3 | `agent-supervisor/scripts/supervisor/session-defaults.sh:8`, `cli.py:83-88`, `closed-report.sh:50`, `watchdog.sh:74`, `digest.sh:42`, `refresh_brief_resume.py:56`, `acceptance.sh:69`, `contest-stop.sh:175` | Every one of these is a **default** (`${VAR:-agent-supervisor}` or a bare literal) feeding a `gh --repo` call or a repo-registry entry. A `gh` call against a repo name that still exists but has been renamed/archived returns an empty or redirected result far more often than a hard error — so these scripts keep running, keep reporting "0 open PRs" or "no work", and nobody notices until a real PR sits unattended | §4/§5/Addendum |
| 4 | `~/.local/state/estate-loop/check.sh` and `tick-scan.sh` (both un-versioned, human/loop-authored, outside either repo) | `check.sh:115` `cd`'s into `/Users/jon/source/repos/Personal/agent-tui` and exits 0 if that path is gone — a "nothing to do" silently indistinguishable from "the loop is broken". `tick-scan.sh:44,72` default `AGENT_SUPERVISOR_REPO` and a `merge-pr.sh` invocation to the pre-merge path | §7 |
| 5 | launchd jobs (`director-loop`, `quota-watch`, `supervisor-heartbeat`, `weekly-watch`) | All four `ProgramArguments` name `/Users/jon/source/repos/Personal/agent-supervisor/scripts/supervisor/*.sh` directly. launchd's own failure behaviour for a missing `Program` is to log to `StandardErrorPath` and retry per `KeepAlive`/`StartInterval` — nothing surfaces to a terminal a human is looking at | §7 |
| 6 | `agent-dotfiles/settings/mcp/servers.json:22` | Points `sync.py`'s MCP merge at the pre-merge `agent-supervisor` path. Currently dormant (not in the live `~/.claude.json` today), so nothing breaks *yet* — but the next `sync.py` run redeploys this file verbatim, at which point it becomes the same silent failure as #5 | §7 |
| 7 | `agent-tui`'s `AGENT_SUPERVISOR_REPO` / `-supervisor-repo` resolution (`cmd/estate/main.go`, `cmd/estate/supervisor.go`) | Genuinely self-healing once repointed at the merged repo's own subtree (it's a runtime-resolved relative path, not a build-time import) — listed here only so it isn't mistaken for urgent; see §5 for why | §5 |

Everything below this table is either loud (a compile error, a 404, a test
failure) or a one-time content-merge choice (two `README.md`s, two
`AGENTS.md`s) that a human necessarily looks at — lower risk by construction,
listed for completeness in §1-§6.

---

## 1. Import paths

**Positive control:** `grep -c "agent-tui" agent-tui/go.mod` → `1`
(`module github.com/jonhill90/agent-tui`) — confirms the module path itself
and the grep mechanism.

- **107 `.go` files** import `"github.com/jonhill90/agent-tui/..."` (measured:
  `grep -rl '"github.com/jonhill90/agent-tui/' --include="*.go" agent-tui | wc -l`).
  Every one breaks **loudly** (`go build` fails) the moment the module path
  changes — this is the easy half the brief itself names.
- **34 distinct import prefixes** (corrected from an earlier draft's `30` —
  flagged independently by two reviewers, `estate:2` and `estate:3`, and
  re-verified here a third time before fixing it): 31 base `internal/`
  packages —
  `internal/admin agents apidocs board chat connectors cost dashboard
  external flow gallery knowledge lane lanechat library mcp mcpservers
  mergepr monitor nav navwalk prverdict rail secrets session shell skills
  sshserver stub theme workflows`
  — (31 names; counted programmatically, not by eye, this time:
  `python3 -c "print(len('<the list above>'.split()))"` → `31`) plus
  `internal/lanechat`'s own three sub-packages
  (`laneprimary`/`roomprimary`/`unifiedlist`), 31 + 3 = 34. Ground truth,
  independent of the enumerated list above: `grep -rho
  '"github.com/jonhill90/agent-tui/[a-zA-Z0-9_/]*"' --include="*.go" .
  | sort -u | wc -l` → `34`, exact match.
- **agent-supervisor side: 0 real imports.** `grep -rl "agent-tui"
  --include="*.go" agent-supervisor` matches exactly 2 files
  (`daemon/cmd/supervisord/main.go`, `daemon/internal/sendmsg/sendmsg.go`),
  both comment-only citations of `agent-tui#14`'s vocabulary — not an
  `import`. Confirmed by reading both matched lines.

**Action:** a single `sed`/`gofmt -r`-style rewrite of the module path and
every one of the 107 files' import blocks, done in the same commit as the
`go.mod` line change (a partial rewrite does not compile, so this has no
"can follow" half).

---

## 2. Build and install targets

**Positive control:** `grep -rn "go build" agent-tui/.github/workflows/ci.yml`
→ 1 hit (line 29) — confirms the grep mechanism before trusting agent-supervisor's
zero, below.

- **agent-tui**, everywhere `go build`/`go install` is invoked outside
  `.go` files themselves:
  - `README.md:99,391` — `go build -o estate ./cmd/estate`, `go build ./...`
  - `AGENTS.md:210,232` — same two forms
  - `.github/workflows/ci.yml:29` — `go build ./...` (`working-directory: agent-tui`)
  - `scripts/render-uivariants.sh:13` — `go build -o "$bin" ./tools/uivariants`
  - `scripts/render-memoryvariants.sh:19` — `go build -o "$bin" ./tools/memoryvariants`
  - `scripts/verify-lanes-unaffected.sh:30` — error text naming
    `go build -o estate ./cmd/estate` as the fix-it hint
  - `docs/SPEC.md:139,319`, `docs/SPEC-shell.md:253`, `docs/lanechat-variant-comparison.md:66-68`,
    `docs/vhscheck-guard.md` (8 mentions), `testdata/vhs/README.md:41` — doc/tape
    references to the same build command, several inside `testdata/vhs/*.tape`
    files that are themselves executed by `vhs` and by `internal/vhscheck`'s
    own guard (see that package's doc comment) — these are **live**, not prose,
    because `internal/vhscheck` fails CI if a tape's own `go build ... ./cmd/<x>`
    target directory doesn't exist.
  - No `Makefile` anywhere in `agent-tui`.
- **agent-supervisor: no `go build`/`go install` anywhere** —
  `grep -rln "go build\|go install" agent-supervisor` (whole tree, excluding
  `.git`) matches exactly one file, `daemon/cmd/supervisord/main_test.go:20`,
  and that line is a comment ("`go build`/`go vet` proves the switch
  compiles..."), not an invocation. **No `Makefile`.** No CI workflow in
  `agent-supervisor` builds, vets, or tests the `daemon` Go module at all —
  confirmed by grepping all four `.github/workflows/*.yml` for
  `go build|go install|go test|go vet`: zero matches. This is a finding in
  its own right, independent of the merge: **`agent-supervisor/daemon` has no
  build gate today**, so the merge doesn't need to reconcile two build
  pipelines here — it needs to notice one never existed and decide whether
  the merged repo's CI should now cover it.

**Action:** once module paths move, every `go build -o estate ./cmd/estate`
literal listed above needs its working directory or path prefix updated to
match wherever `cmd/estate` lands inside the merged tree. This is a find-list,
not a design decision — the merge's own layout choice (§3 below) determines
the actual replacement path.

---

## 3. CI workflows

**Positive control:** filename listing itself is the control — both trees
were `ls`'d directly, not grepped, so there is no false-zero to guard
against; the finding below is the exhaustive list.

- **No filename collision.** `agent-tui/.github/workflows/`: `ci.yml`.
  `agent-supervisor/.github/workflows/`: `completion-gate.yml`,
  `fixpass-evidence.yml`, `ui-evidence.yml`, `validate.yml`. Five distinct
  names, no overwrite-on-merge.
- **The one workflow that would silently pass while testing nothing:
  `agent-tui/.github/workflows/ci.yml`.** Full content at lines 11-31: it
  checks out `agent-tui` itself at `path: agent-tui`, **then separately
  checks out `jonhill90/agent-supervisor` at `path: agent-supervisor`**
  (lines 17-20), and runs `go build|vet|test ./...` with
  `working-directory: agent-tui` and `AGENT_SUPERVISOR_REPO:
  ${{ github.workspace }}/agent-supervisor` (line 31) — this is what makes
  `internal/lane/states_lanessh_test.go`'s cross-check against `lanes.sh`
  actually run in CI instead of skipping. **After the merge, if this
  workflow is not rewritten, the second `checkout@v4` step either 404s
  (loud — acceptable) or, if `jonhill90/agent-supervisor` is kept around
  as an archived read-only remnant instead of deleted, silently pulls a
  frozen pre-merge snapshot that no longer matches whatever `daemon/`
  or `scripts/supervisor/` looks like in the merged repo — CI stays green
  while validating a cross-check against code nobody can see or edit
  anymore.** This is ranked #2 above for exactly that reason.
- `agent-supervisor`'s four workflows assume the repo name only through
  `github.token`/`github.repository`-scoped API calls (`gh api
  "repos/${{ github.repository }}"`-shaped, e.g. `fixpass_evidence_gate.py
  --repo "${{ github.repository }}"` in `fixpass-evidence.yml`) — these
  **self-heal automatically** on rename, since `github.repository` resolves
  to whatever the workflow's own repo is called at run time. No entry in
  any of the four names `agent-supervisor` as a literal string in a way that
  would break. Confirmed by reading all four files in full (see §4's
  transcript for the content).

**Action:** rewrite `ci.yml`'s two-checkout shape into a single-checkout,
single-`working-directory` (or no `working-directory` at all) job once the
merge actually puts both trees in one checkout; delete the
`AGENT_SUPERVISOR_REPO` env line's cross-repo indirection since the test
this enables (`states_lanessh_test.go`) can then point at a same-checkout
relative path instead of a second clone.

---

## 4. Scripts trees

**Positive control:** `find agent-tui/scripts -type f | wc -l` → `4`,
`find agent-supervisor/scripts -type f | wc -l` → `105` — both nonzero,
confirms `find` reached both trees before trusting the collision check below.

- **No exact relative-path collisions.** `comm -12` between the two trees'
  full relative file listings (`scripts/**` in both) returns **zero** lines —
  the two `scripts/` trees can be concatenated (e.g. `agent-supervisor`'s
  105 files moved under `scripts/supervisor/` as they already mostly are,
  `agent-tui`'s 4 alongside) with no individual file overwriting another.
  The collision named in the brief's measured facts is at the directory
  name only (`scripts/` exists in both), not deeper.
- **agent-tui scripts referencing agent-supervisor or a sibling path:**
  `scripts/verify-lanes-unaffected.sh` is the only one
  (`grep -ln "agent-supervisor\|\.\./agent"` — 1 file). It references
  `$AGENT_SUPERVISOR_REPO`/`-supervisor-repo` indirectly through the built
  binary's own flag, not a literal sibling-repo path in the shell script
  itself.
- **agent-supervisor scripts with a hardcoded sibling-repo path or GitHub
  slug** (code lines only, comment-only `agent-supervisor#NNN` issue
  citations excluded — full file:line list, all confirmed by direct read,
  not grep-count alone):
  - `scripts/supervisor/cli.py:83-88` — `DEFAULT_REPOSITORIES`, the
    canonical name→path→github triple registry:
    `agent-dotfiles`, `agent-supervisor`, `skills`, `skills-private`,
    `agent-evals`. **`agent-tui` is not in this registry at all** — matches
    the ledger finding in §7 that no `lanes.repo` row ever names an
    `agent-tui` checkout either; agent-tui's own lanes are dispatched
    through a different path (this repo, under the `estate:N` lane naming
    this very inventory was requested under).
  - `scripts/supervisor/closed-report.sh:50` — `REPOS` default includes
    `jonhill90/agent-supervisor` and `jonhill90/agent-tui` literally.
  - `scripts/supervisor/watchdog.sh:74` — `SUPERVISOR_REPOS` default:
    `agent-dotfiles agent-supervisor skills skills-private agent-evals`
    (no `agent-tui`).
  - `scripts/supervisor/digest.sh:42` — same list, same omission.
  - `scripts/supervisor/refresh_brief_resume.py:56` — same list as a
    Python tuple.
  - `scripts/supervisor/acceptance.sh:69` — `REPO` default:
    `jonhill90/agent-supervisor`.
  - `scripts/supervisor/contest-stop.sh:175` — hardcoded
    `jonhill90/agent-supervisor` as a `dispatch.sh` argument.
  - `scripts/supervisor/session-defaults.sh:8` — `AGENT_SUPERVISOR_DEFAULT_LANES_SESSION`
    defaults to the literal tmux session name `agent-supervisor`. **Lower
    priority than it looks:** `session_for_repo()` (same file, below line 8)
    derives the actual per-repo tmux session name dynamically from whichever
    repo a lane is dispatched against, and always wins when a repo is known;
    this default only fires for callers with no repo context at all (e.g.
    `director`). Self-describing bug precedent already in this file's own
    header comment: *"The default is centralized because a repo rename left
    a dozen independent literals disagreeing about where new work should
    land."* — this is that exact defect class, one rename later.
  - `scripts/supervisor/quota-watch.sh:71` — `TARGET` default:
    `agent-supervisor:@1`, a tmux target coupled to whatever the live
    session ends up named (see previous bullet — self-healing only if the
    tmux session keeps being named `agent-supervisor` post-merge; breaks if
    the session itself gets renamed to `agent-estate`).
  - `scripts/supervisor/dispatch.sh:375` — one runtime error string names
    `agent-supervisor#17` (issue citation inside an error message, not a
    path) — low priority, cosmetic only.

**Action:** the `cli.py` registry (line 83-88) is the one place a single
edit fixes the most call sites — everything in `core.py`/`cli.py` that
resolves `--repo <name>` against `DEFAULT_REPOSITORIES` inherits the fix
for free. The remaining bullets are independent literals (the same shape
`session-defaults.sh`'s own comment already warns about) and need one edit
each; none of them share a helper today.

---

## 5. The 54(/59-measured)-file coupling, broken down by kind

The brief's headline number was 54; re-measuring the same direction fresh
(`grep -rl "agent-supervisor" --include="*.go" agent-tui | wc -l`) returns
**59** — both are real counts, the difference is almost certainly which
commit each was taken against (this repo is under active multi-lane
development; +5 files over a few days of merged PRs is unsurprising, not a
methodology error). **Positive control:** the same grep for `"agent-tui"` in
`agent-supervisor/*.go` returns exactly 2, both already confirmed
comment-only in §1 — proves the direction and the tool work before trusting
the 59.

Breaking the 59 down by what they actually do (a file can be in more than
one bucket):

| kind | count | cost to merge | representative files |
|---|---|---|---|
| Comment/issue-citation only (`agent-supervisor#NNN`, "lanes.sh's own vocabulary") | ~26 | **zero** — text, not code | `internal/lane/glyph.go` (4 mentions, zero code coupling — confirmed by direct read), most of `internal/rail`, `internal/board` doc comments |
| Live `AGENT_SUPERVISOR_REPO` / `-supervisor-repo` flag resolution | 3 non-test (`cmd/estate/main.go`, `cmd/estate/supervisor.go`) + 3 test-only (`internal/lane/states_lanessh_test.go`, `internal/mcp/client_test.go`, `internal/apidocs/load_test.go`) | **a bug if wrong, not a compile error** — the flag/env var is a runtime string; pointing it at the wrong directory degrades to "supervisor repo not found" (agent-tui#49's already-fixed degraded-Home-pane path), not a crash | `cmd/estate/main.go:75`, `cmd/estate/supervisor.go:22` |
| Subprocess exec of a path under `agent-supervisor`'s own `scripts/supervisor/` tree | 33 files mention `lanes.sh`/`scripts/supervisor`, but **only 2 actually construct and exec such a path**: `cmd/estate/supervisor.go:22` (`filepath.Join(dir, "scripts", "supervisor", "mcp_server.py")`, existence-check to confirm a real supervisor checkout) and `cmd/estate/main.go:253` (`filepath.Join(supervisorRepoResolved, "scripts", "supervisor", "quota.sh")`, the cost pane's quota source). The other 31 files in this 33 only *mention* `lanes.sh` or `scripts/supervisor` in a comment explaining provenance (e.g. `internal/lane/states.go`'s own state table citing `lanes.sh`'s `state=` assignments) | **self-healing once repointed** — these are relative `filepath.Join`s off one resolved root (`supervisorRepoResolved`), not per-call hardcoded absolute paths; repointing the one flag/env var fixes both | `cmd/estate/supervisor.go:22`, `cmd/estate/main.go:253` |
| MCP subprocess client itself | 1 (`internal/mcp/client.go`) | same as above — spawns `python3 <resolved-root>/scripts/supervisor/mcp_server.py`, inherits whichever root `main.go` resolved | `internal/mcp/client.go` |

**The distinction that matters, stated plainly per the brief's own
instruction:** of the 59 files, **2** do the actual work of finding and
executing a path inside `agent-supervisor` (`supervisor.go`, `main.go`'s
`quota.sh` join), 1 more is the client that subprocess actually runs
(`internal/mcp/client.go`), and **~26 are pure prose** that costs nothing to
carry into the merge unedited. The other ~30 are provenance comments
attached to genuine *logic* ports (state tables, glyph sets) that were
already fully vendored into agent-tui's own code — they cite
`agent-supervisor` as where the idea came from, not as something the merged
binary still reaches out to.

---

## 6. Docs cross-references

**Positive control:** `grep -rln "agent-supervisor" agent-tui/docs` → 4
files, confirms the docs tree is searchable before trusting the "zero real
links" finding below.

- **Zero relative-path or `github.com` hyperlinks between the two docs
  trees in either direction.** `grep -rn "\.\./agent-supervisor\|github\.com/jonhill90/agent-supervisor"
  agent-tui/docs` and the mirror search in `agent-supervisor/docs` both
  return **zero** matches — the 4 agent-tui docs
  (`PRD.md`, `SPEC.md`, `SPEC-shell.md`, `SPEC-agentbox-execution-mode.md`)
  and 2 agent-supervisor docs (`docs/runbooks/send-keys-retirement-284.md`,
  `docs/product/SPEC.md`) that do mention the other repo's name do so as
  plain-text issue citations (`agent-supervisor#153`) or prose ("moved to
  `jonhill90/agent-supervisor`"), never as a markdown link or URL a merge
  would need to repoint.
- **No relative-path collisions inside either `docs/` tree**
  (`comm -12` between the two full file listings: zero, same check as §4).
- **Competing index MOCs, corrected from the brief's framing:**
  `agent-tui/docs/index.md` exists and is scoped to `agent-tui` alone (its
  own content: PRD/SPEC/SPEC-shell/research links, no claim to be an
  estate-wide index). **`agent-supervisor` has no `docs/index.md` at all** —
  confirmed by `find agent-supervisor -maxdepth 2 -iname "index*"`, zero
  results. Its closest analog is `scripts/supervisor/README.md` (800+ lines,
  an operation/write-path reference table) and the top-level `README.md`.
  So there are not two competing MOCs today — there is one MOC
  (`agent-tui/docs/index.md`) and one large reference doc playing a
  different role. The actual top-level collision is exactly what the
  brief's measured facts already named: `README.md`, `AGENTS.md`,
  `CLAUDE.md` exist in both repos and will need a human content-merge
  decision each, not a mechanical move.

**Action:** none of this needs a mechanical rewrite. It needs one decision
per top-level colliding filename (`README.md`, `AGENTS.md`, `CLAUDE.md`,
`docs/`, `scripts/`, `.github/`) about which content wins, gets concatenated,
or gets demoted to a subsection — a content decision, not a path-breakage
one, so it's out of this inventory's scope beyond flagging it exists.

---

## 7. External pins

### 7a. The ledger — `~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3`

**Positive control:** `SELECT count(*) FROM lanes` → 298 (nonzero, proves
the DB is live and queryable) before trusting the "0 agent-tui rows" finding
below.

- **`lanes.repo`** (the table dispatch actually reads to know where a lane's
  own worktree lives): two canonical, currently-active, non-temp-directory
  values —
  - `/Users/jon/source/repos/Personal/agent-dotfiles` — 5 lanes (`agent-dotfiles:6,7,8,9,10`)
  - `/Users/jon/source/repos/Personal/agent-supervisor` — 4 lanes (`estate:2,3,4,5`,
    all updated within the last hour of this measurement — this inventory's
    own lane, `estate:4`, is one of them)
  - **Zero rows name `agent-tui`** — consistent with §4's finding that
    `agent-tui` is absent from `cli.py`'s own `DEFAULT_REPOSITORIES`
    registry; agent-tui work is dispatched under `estate:N` lanes whose
    `lanes.repo` points at `agent-supervisor`'s own checkout, not a
    dedicated `agent-tui` checkout row.
  - Every other `lanes.repo` value is a per-task ephemeral `/tmp` or
    `/private/var/folders/.../T/` worktree path — regenerated fresh per
    dispatch, nothing to fix.
  - **This is ranked #1 in the top table**: `lanes.repo`'s two live,
    non-ephemeral values are exactly the "stale path in `lanes`... breaks
    dispatch with no error message" case the brief calls the worst failure
    mode, and it is currently populated with the pre-merge path.
- **`pr_verdicts.repo`** — 1 distinct value, `jonhill90/agent-supervisor`
  (1 row). **`pr_authorship.repo`** — 1 distinct value, same,
  5 rows. Both are **(c) historical record** — each row is keyed to a real
  PR that really was reviewed/authored against that repo name at the time;
  rewriting them to say `agent-estate` would misrepresent what actually
  happened. Any *code* that queries these tables by repo name going forward
  needs to know both the old and new name (or match on PR number alone) —
  that's a code change in `internal/prverdict`/`internal/mergepr` (agent-tui)
  and `scripts/supervisor/verdict.py`/`ci_gate.py` (agent-supervisor), not a
  ledger rewrite.
- **`source_tasks.source_url`** — 706 rows reference `agent-supervisor`
  issue/PR URLs, 115 reference `agent-tui` ones (`github.com/jonhill90/<repo>/issues|pull/N`).
  **(c) historical record** — same reasoning: these URLs are what was true
  when the row was written. GitHub itself will 302-redirect an
  `issues/N` URL through a rename in the common case (confirm this
  empirically before relying on it, this inventory did not check GitHub's
  live redirect behavior for a renamed-and-merged repo specifically, which
  is a different operation than a plain rename).
- **`items.body` / `prompts.text_raw`/`text_clean`** — 31 / 447 rows mention
  `agent-supervisor`. **(c) historical record**, explicitly per this
  project's own stated convention (`CLAUDE.LOCAL.md`'s "never rewrite
  history to match a rename" rule, restated in the brief's ADDENDUM) —
  do not touch these.
- **`components` table** — 0 rows. Checked, not silently skipped: schema
  has a `name` column that could theoretically hold a repo name, but the
  table is currently empty.

### 7b. launchd (4 live jobs, all pointing at the pre-merge path)

All confirmed by direct plist read, not grep alone:

| plist | `ProgramArguments` | cadence |
|---|---|---|
| `~/Library/LaunchAgents/com.jonhill.director-loop.plist` | `/bin/bash /Users/jon/source/repos/Personal/agent-supervisor/scripts/supervisor/director-loop.sh --target director:@35` | every 900s |
| `~/Library/LaunchAgents/com.jonhill.quota-watch.plist` | `/bin/bash .../agent-supervisor/scripts/supervisor/quota-watch.sh --once --target agent-supervisor:@13` | every 300s |
| `~/Library/LaunchAgents/com.jonhill.supervisor-heartbeat.plist` | `/bin/bash .../agent-supervisor/scripts/supervisor/heartbeat.sh --once --target director:@3` | every 900s |
| `~/Library/LaunchAgents/com.jonhill.weekly-watch.plist` | `/bin/bash .../agent-supervisor/scripts/supervisor/weekly-watch.sh` | every 1800s |

A 5th job, `com.jonhill.supervisor-watchdog.plist`, points at
`/Users/jon/.local/state/agent-dotfiles-supervisor/live/scripts/supervisor/watchdog.sh`
— a **separately deployed clone** of `agent-supervisor` (confirmed:
`git remote -v` inside that directory → `jonhill90/agent-supervisor.git`),
not the canonical checkout under `~/source/repos/Personal/`. Renaming or
moving the canonical checkout does **not** break this job by itself; this
deployed clone needs its own, separate re-sync after the merge (however that
deploy step currently works — not traced further here, out of this
inventory's read-only scope).

`crontab -l` is empty (confirmed, not assumed) — no cron entries to fix.

### 7c. `~/.local/state/estate-loop/` (un-versioned, outside both repos)

- `check.sh:115` — `cd /Users/jon/source/repos/Personal/agent-tui || exit 0`
  — silently exits clean if the path is gone, indistinguishable from "no
  work found".
- `check.sh:57,149,164,169,170,228` — `gh pr list --repo jonhill90/agent-tui`,
  a `for repo in jonhill90/agent-tui jonhill90/agent-supervisor ...` loop,
  and a hardcoded call to
  `/Users/jon/source/repos/Personal/agent-supervisor/scripts/supervisor/merge-pr.sh`.
- `tick-scan.sh:44,68,72,77,88` — `AGENT_SUPERVISOR_REPO` default path,
  `gh pr list --repo jonhill90/agent-supervisor`, the same `merge-pr.sh`
  call, and a `for repo in jonhill90/agent-tui jonhill90/skills
  jonhill90/agent-dotfiles` loop.
- `status.sh:48` — `for r in agent-tui agent-supervisor skills agent-dotfiles agent-evals`.
- `build-loop.md` and ~130 other `.md` files under this directory are
  one-shot dispatch briefs — human/agent-read-once, not re-executed —
  **(c)-equivalent**, leave as-is.

### 7d. `agent-dotfiles/settings/mcp/servers.json:22`

`"/Users/jon/source/repos/Personal/agent-supervisor/scripts/supervisor/mcp_server.py"`
under `mcpServers.supervisor`. This is the source `scripts/sync.py` deep-merges
into `~/.claude.json`, `~/.copilot/mcp-config.json`, and `~/.codex/config.toml`.
**Currently dormant** — the live `~/.claude.json`'s `mcpServers` block today
has only `context7`, `microsoft-learn`, `deepwiki`; no `supervisor` entry is
deployed right now, so nothing is broken *yet*. It becomes live again the
next time `sync.py` runs, at which point it silently deploys the pre-merge
path everywhere it merges to.

### 7e. `Skills` (`jonhill90/skills`) and everything else swept

**No live coupling found.** Every `agent-supervisor`/`agent-tui` mention
across `~/source/repos/Personal/Skills`, `~/.claude/` (CLAUDE.md,
settings.json, settings.local.json, skills/, agents/, commands/; `hooks/`
does not exist there), `~/.claude.json`'s `mcpServers` block, and the
remaining `~/.local/state/*` directories is either a GitHub issue citation,
a provenance comment ("ported from agent-supervisor's ..."), an explicit
`/path/to/...` placeholder in a usage example, or GitHub-URL-keyed cached
data (`~/.claude/gh-pr-status-cache.json`) — none of it reads a filesystem
path that the merge would move. Positive controls run per location before
trusting each zero/low count: `grep -rl "skill"` against `Skills` (matched),
`grep -rl "claude"` against `agent-dotfiles` (matched), `grep -n "claude"
~/.claude/settings.json` (matched line 3), `grep -rl "claude"` against
`~/.local/state/estate-loop` (matched) and `~/.local/state/hill90-codex-supervisor`
(matched) before their own zero results were trusted.

---

## Addendum classification — every live `agent-supervisor` hit, (a)/(b)/(c)

Per the ADDENDUM's own instruction: classify each LIVE hit (PROSE MENTION
entries above are already out of scope for this table — they cost nothing
and are not listed again here) as **(a)** must change before the rename
lands, **(b)** must change but can follow, or **(c)** must not change
(historical record), and rank (a) by loud vs. silent.

| # | Location | Class | Loud/Silent | Why |
|---|---|---|---|---|
| 1 | `ledger.lanes.repo` rows (agent-dotfiles, agent-supervisor) | (a) | **Silent** | Dispatch target resolution; §7a |
| 2 | `agent-tui/.github/workflows/ci.yml:11-31` two-checkout shape | (a) | **Silent** (worse: green-but-wrong) | §3 |
| 3 | 4 launchd plists (`director-loop`, `quota-watch`, `supervisor-heartbeat`, `weekly-watch`) | (a) | **Silent** (logs to a file nobody watches) | §7b |
| 4 | `~/.local/state/estate-loop/check.sh`, `tick-scan.sh`, `status.sh` | (a) | **Silent** (`exit 0` on missing path reads as "no work") | §7c |
| 5 | `agent-supervisor/scripts/supervisor/cli.py:83-88` `DEFAULT_REPOSITORIES` | (a) | Loud-ish (a `--repo` lookup miss is a KeyError-shaped failure in most callers, not silently wrong data) — not independently traced to every call site in this read-only pass | §4 |
| 6 | `closed-report.sh:50`, `watchdog.sh:74`, `digest.sh:42`, `refresh_brief_resume.py:56`, `acceptance.sh:69`, `contest-stop.sh:175` | (a) | **Silent** — a `gh --repo <renamed>` call degrades to empty results far more often than a hard error | §4 |
| 7 | `agent-dotfiles/settings/mcp/servers.json:22` | (b) | Silent, but dormant today (§7d) — fix before the next `sync.py` run, not necessarily before the rename itself | §7d |
| 8 | `agent-tui`'s `AGENT_SUPERVISOR_REPO`/`-supervisor-repo` resolution (`main.go`, `supervisor.go`) | (b) | Loud (degrades to agent-tui's own already-shipped "supervisor repo not found" Home-pane state, agent-tui#49) — self-healing once the one flag/env default is repointed | §5 |
| 9 | `session-defaults.sh:8` tmux session default, `quota-watch.sh:71` tmux target default | (b) | Silent but low-blast-radius — only fires with no repo context, or if the tmux session itself is also renamed | §4 |
| 10 | `dispatch.sh:375` error-message issue citation | (b) | Loud but cosmetic (an error message's own text, not logic) | §4 |
| 11 | `ledger.pr_verdicts.repo`, `pr_authorship.repo`, `source_tasks.source_url`, `items.body`, `prompts.text_*` | (c) | n/a — historical record, do not rewrite | §7a |
| 12 | `~/.local/state/estate-loop/*.md` dispatch briefs (~130 files) | (c) | n/a — one-shot, human/agent-read-once records | §7c |

**Note on the module path `agent-supervisor/daemon`:** per the brief, this
was never a canonical path (no `github.com/...` prefix) and needs fixing
independent of the rename — folded into §1/§2's "no `go.mod` module-path
convention exists in agent-supervisor today" finding rather than repeated as
its own numbered item, since the fix is the same action (pick and apply a
real module path) regardless of what triggers it.
