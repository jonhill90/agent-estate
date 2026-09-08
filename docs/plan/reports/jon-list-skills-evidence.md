# Jon-list evidence: `diagram-design` and `tmux` — Push 4 follow-through

Evidence only. No decision made, nothing moved, nothing deleted, no
symlinks touched, no commits made anywhere. Gathered against real
commands on this machine, 2026-09-07. `agent-dotfiles`'s local checkout
was not touched (unpushed write-guard commit + uncommitted edits, owner's
to resolve, not a lane's).

Source of the two items: `docs/skills-reconciliation.json` on
`jonhill90/skills` `origin/main` (merged #304),
`environment_counts.installed_outside_public=2`,
`third_party_installed=1`. Both currently installed at
`~/.claude/skills/<name>` as a symlink to
`../../.agents/skills/<name>`.

## What `~/.agents/skills` actually is (shared by both items)

```
$ cd ~/.agents && git rev-parse --is-inside-work-tree
fatal: not a git repository (or any of the parent directories): .git
$ find ~/.agents -maxdepth 2 -name ".git"
(nothing)
$ ls -la ~/.agents
.DS_Store
.skill-lock.json
skills/
```

**Not a git repo, not a checkout of anything.** Plain filesystem
directory, 16 skill subdirectories, one JSON file (`.skill-lock.json`)
that carries per-skill install provenance: source repo, source type,
source URL, the exact `skillPath` within that source, an install-time
`skillFolderHash`, and `installedAt`/`updatedAt` timestamps. This is a
neutral, cross-harness skill mirror populated by two independent
mechanisms:

1. **agent-dotfiles' own sync** (`scripts/sync.py`'s
   `ensure_neutral_skills()`) mirrors the Tier-A "neutral trio" roster —
   the 12 names listed in `settings/default-skills.txt` and in
   `~/.apm/apm.yml`'s own dependency list — into this directory so
   Pi/Codex/Copilot (which don't share Claude's own skill-install path)
   all see the same set. `tmux` is on that roster (`default-skills.txt`
   line 13).
2. **A separate installer** (the tool that writes `.skill-lock.json` —
   its own `lastSelectedAgents` field lists `amp, antigravity,
   antigravity-cli, cline, codex, cursor, deepagents, gemini-cli,
   github-copilot, kimi-code-cli, opencode, warp, zed, claude-code`,
   i.e. a general multi-harness skill installer, not agent-dotfiles'
   own script) fetches named third-party/GitHub skills directly and
   records exact provenance. `diagram-design` came in this way; `tmux`
   also has a lock entry (see below) even though it's *also* on the
   agent-dotfiles roster — the lock records whichever mechanism most
   recently touched that name.

Claude Code's own `~/.claude/skills/<name>` symlink, for these two
names specifically, resolves through this neutral mirror rather than
directly into the `jonhill90/skills` repo checkout the other 40 use.

---

## `diagram-design`

### (1) Provenance — KNOWN, from `.skill-lock.json`

```json
"diagram-design": {
  "source": "cathrynlavery/diagram-design",
  "sourceType": "github",
  "sourceUrl": "https://github.com/cathrynlavery/diagram-design.git",
  "skillPath": "skills/diagram-design/SKILL.md",
  "skillFolderHash": "08c8d2b76db8f779a00e09c58abe20e63054a70b",
  "installedAt": "2026-08-18T02:44:59.673Z",
  "updatedAt": "2026-08-18T02:44:59.673Z"
}
```

Independently corroborated — the upstream repo is real, public, and
matches:

```
$ gh api repos/cathrynlavery/diagram-design
full_name:   cathrynlavery/diagram-design
license:     MIT
description: 38 editorial diagram types for Claude Code, Codex, and Pi.
             Self-contained HTML + SVG. No shadows. No Mermaid slop.
html_url:    https://github.com/cathrynlavery/diagram-design
```

`SKILL.md`'s own frontmatter independently states `license: MIT`,
matching. No standalone `LICENSE` file inside the installed skill
directory itself (`find ... -iname "LICENSE*"` → empty) — the license
declaration lives only in `SKILL.md`'s frontmatter and the upstream
repo's own GitHub-reported license. No "adapted from" / "based on" /
original-author prose found inside the skill body beyond that.

**This is third-party content, clearly attributed, not something to
silently treat as Jon's or the estate's own.** Per the standing rule
(it-957502d135216f90), that determination is stated here with evidence
and stops — the disposition is Jon's.

### (2) Employer/work-content suspicion — FLAG, checked, none found by this pass

```
$ grep -rli "employer|confidential|proprietary|nda\b|internal use only|do not distribute" \
    ~/.agents/skills/diagram-design
(no output)
```
No LICENSE-adjacent or confidentiality marker found by this targeted
keyword pass. This is not an exhaustive content review — stating the
grep terms used so the check's own limits are visible, not implying a
clean bill beyond what was actually searched. Two "machine-path"
findings the reconciliation tool's own packaging audit flagged were
inspected directly and are generic tilde-relative documentation paths
(`~/.pi/agent/skills/...`, `~/.diagram-design/profiles/...`), not
Jon's actual filesystem paths or anything employer-identifying.

### (3) Also in the public Skills repo or skills-private? — KNOWN: no, neither

```
$ gh api repos/jonhill90/skills/contents/skills/diagram-design
404 Not Found

$ find ~/source/repos/Personal/skills-private -iname "*diagram*"
(nothing)
$ ls ~/source/repos/Personal/skills-private/skills/
source-probe
```
`skills-private` holds exactly one skill (`source-probe`), unrelated.
No byte comparison possible or needed — there is no second copy
anywhere in either tracked repo to diverge from. The only copy is the
one at `~/.agents/skills/diagram-design`, sourced from the third-party
upstream above.

### (4) What would break if repointed or removed — KNOWN, and partially UNKNOWN

**KNOWN:** No hard-coded reference to the name `diagram-design` exists
in any Go source in `agent-estate`'s TUI (`grep` across
`src/tui/**/*.go`, excluding testdata, returned nothing for this name).
It does **not** appear in agent-dotfiles' `settings/default-skills.txt`
— its presence in `~/.agents/skills` is not part of the roster
agent-dotfiles' own sync manages, so removing it would not be
"corrected back" by a future `sync.py` run the way a roster skill
would be. Several hits across `run/` and TUI `testdata/vhs/*/observations/skills.jsonl`
files are historical VHS-walk observation logs (recordings of what a
past manual TUI verification pass rendered) — read-only fixtures, not
live assertions; nothing in the TUI's own Go test suite asserts on this
name.

**UNKNOWN:** Whether any *other* machine, harness config, or private
material references `diagram-design` by name was not exhaustively
checked beyond this machine's `agent-estate`, `agent-dotfiles`, and
`agent-estate-lanes` checkouts and the `run/` coordination tree.
Whether it is actively invoked in real sessions (vs. installed but
unused) was not measured — the reconciliation manifest's own `eval`
field for it is honestly `no-evals` / `status: no-evals` (no bundled
evaluation record).

### (5) KNOWN vs UNKNOWN, stated plainly

**KNOWN:** Genuine third-party skill (MIT-licensed, `cathrynlavery/diagram-design`,
independently verified to exist on GitHub with matching license and
description). Installed 2026-08-18 via a general multi-harness skill
installer, not agent-dotfiles' own sync. Does not exist in either
`jonhill90/skills` or `skills-private`. Not referenced by name in any
checked Go source or roster file. Already a two-week-old, previously
recorded finding — see the cross-cutting note below (jonhill90/skills#282).
No confidentiality/employer marker found by the targeted grep used.

**UNKNOWN:** Whether it is actually used in practice. Whether any
material outside this machine's checked repos references it. Whether
the upstream repo's current HEAD still matches the installed
`skillFolderHash` (not independently re-fetched — the lock file's hash
was taken as recorded, not re-verified against upstream bytes today).

---

## `tmux`

### (1) Provenance — KNOWN, from `.skill-lock.json` AND git history

```json
"tmux": {
  "source": "jonhill90/skills",
  "sourceType": "github",
  "sourceUrl": "https://github.com/jonhill90/skills.git",
  "skillPath": "skills/tmux/SKILL.md",
  "skillFolderHash": "776a0d5ef26f0818fb817b8c349c63c11cf76c3a",
  "installedAt": "2026-08-23T04:54:24.142Z",
  "updatedAt": "2026-08-23T04:54:24.142Z"
}
```

The lock file itself says this install came **from the public Skills
repo**, not a third party. First-commit provenance in that repo:

```
$ git log --reverse --diff-filter=A --format="%H %ad %an <%ae>" --date=iso \
    -- skills/tmux/SKILL.md
acac1f6cafed04f51f3a2af166879c792ae65866 2026-07-26 15:56:27 -0400 Jon Hill <jonhill90@live.com>
```

First-committed by Jon Hill directly. This matches the reconciliation
manifest's own `authorship.class: jon-or-agent-attributed`, with its
own honestly-stated caveat carried forward unchanged: *"Earliest
available addition commit; git attribution does not prove absence of
upstream copying."* Nothing found in this evidence pass contradicts or
confirms that caveat further — stated as the manifest states it, not
strengthened or weakened here.

### (2) Employer/work-content suspicion — FLAG, checked, none found by this pass

```
$ grep -rli "employer|confidential|proprietary|nda\b|internal use only|do not distribute" \
    ~/.agents/skills/tmux
(no output)
```
Same caveat as above: targeted keyword pass, not exhaustive. Content is
generic tmux-agent-supervision material (pane targeting, state
inspection, recovery); no employer- or company-identifying content
observed while reading the files directly for this task.

### (3) Also in the public Skills repo? — KNOWN: yes, near-byte-identical

```
$ diff -rq skills/tmux ~/.agents/skills/tmux      # cloned repo vs installed
Only in skills/tmux/references: eval-result.md
```
Per-file sha256, every file checked individually:

| File | Match? |
|---|---|
| `SKILL.md` | MATCH |
| `references/fundamentals.md` | MATCH |
| `references/supervisor-lanes.md` | MATCH |
| `references/eval-result.md` | **absent locally** (repo has it, mirror doesn't) |
| `scripts/find-sessions.sh` | MATCH |
| `scripts/self-test.sh` | MATCH |
| `scripts/supervisor-watch.sh` | MATCH |
| `scripts/wait-for-text.sh` | MATCH |

**The `install_state: divergent-or-unresolved-install` the
reconciliation manifest reports is real but narrow: exactly one file,
`references/eval-result.md`, differs — every actual instructional file
(`SKILL.md`, both other references, all four scripts) is byte-identical
to the current public repo.** This is not a content fork or third-party
substitution; it is ordinary mirror staleness. Timing confirms why:

```
$ git log -1 --format="%H %aI" -- skills/tmux/references/eval-result.md
2265bad90720809bda5027a8fed42f646409a965 2026-08-23T01:04:14-04:00
```
That commit landed at `2026-08-23T05:04:14Z`; the local mirror's own
`installedAt` is `2026-08-23T04:54:24.142Z` — about **ten minutes
earlier**. The mirror captured `tmux/` before `eval-result.md` existed
in the repo and has simply never been refreshed since.

Not in `skills-private` (confirmed above — its one skill is
`source-probe`, unrelated).

### (4) What would break if repointed or removed — KNOWN, and partially UNKNOWN

**KNOWN:** `tmux` is on agent-dotfiles' own roster
(`settings/default-skills.txt` line 13) and in `~/.apm/apm.yml`'s
dependency list — it is a deliberately-mirrored neutral-trio skill, not
an accidental external link. The `.skill-lock.json`'s own
`lastSelectedAgents` names 14 harnesses configured to read from this
same neutral mirror; several of them (Pi, Codex, Copilot per
agent-dotfiles' own stated purpose for this mirror) have no other
install path for this skill on this machine — they read `~/.agents/skills/tmux`
directly, not through Claude's symlink. Repointing *Claude's* symlink
to the repo directly would not by itself break those other harnesses
(they don't go through `~/.claude/skills/` at all), but it would leave
Claude and the other harnesses reading two different copies of the
skill again unless the neutral mirror is refreshed too. Two Go test
files reference the string `"tmux"` (`admin.go`'s `KnownDependencies`
list, checking whether the `tmux` **CLI binary** is installed — an
unrelated coincidental name match, not the skill; and
`invocations_test.go`'s mocked transcript fixtures, which use `"tmux"`
as an arbitrary example skill-invocation name against synthetic data,
never reading the real installed directory). Neither would fail if the
skill directory moved.

**UNKNOWN:** Whether any live session mid-flight currently holds
`~/.claude/skills/tmux` open by resolved path (a symlink repoint would
be invisible to it, but a plain directory move could not be tested
without actually doing it, which this task does not do). Whether
Pi/Codex/Copilot's own skill-loading code caches the resolved path
rather than re-reading the symlink each session was not checked here —
that would require exercising those harnesses directly, out of scope
for a read-only evidence pass.

### (5) KNOWN vs UNKNOWN, stated plainly

**KNOWN:** Sourced from Jon's own public `jonhill90/skills` repo
(explicit lock provenance, corroborated by git first-commit
attribution to Jon Hill). Every substantive file is byte-identical to
the current repo copy; the one file that differs (`eval-result.md`) is
missing locally because it was added to the repo roughly ten minutes
after this mirror was captured — ordinary staleness, not a fork.
Deliberately mirrored via agent-dotfiles' own roster mechanism for
cross-harness (non-Claude) access, not an accidental orphan install.

**UNKNOWN:** Whether the ten-minute-younger `eval-result.md` gap has
been re-synced since 2026-08-23 by any later sync run (not measured
here — would require re-running `apm compile`/`sync.py`'s mirror step
and diffing again, out of scope for a read-only pass). Live-process
handle behavior under a repoint, as above.

---

## Cross-cutting note

Both items were already partially known institutional knowledge before
this task: a vault fact dated 2026-08-24 (superseded 2026-08-25,
`run/astra-w1/batch-06/.../installed-skills-are-frozen-copies-not-the-repo.md`)
already recorded *"`diagram-design` is installed from `../../.agents/skills`
without being in the repo"* and tracked it as `jonhill90/skills#282`.
This evidence pass corroborates that finding independently, adds the
`tmux` side (not covered by that earlier note), and adds exact
checksums, timestamps, and upstream-repo confirmation neither prior
note carried.

## What was NOT done (per the hard constraints)

- No symlink was repointed, removed, or otherwise touched.
- No file was moved or deleted.
- No commit was made in any repository.
- `agent-dotfiles`'s local checkout was not opened, read, or touched —
  its unpushed write-guard commit and uncommitted guard edits are its
  owner's to resolve.
- No disposition decision was made for either skill. Both stop at
  evidence, per it-957502d135216f90 and the standing "deletions are
  always Jon's list" rule.
