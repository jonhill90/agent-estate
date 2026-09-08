Verdict: APPROVE
Review-Lane: agent-estate:1

Cross-lane review of `run/p12-skills-disposition.md` (lane-c, agent-estate:3),
`jonhill90/skills` docs/ disposition table, P12 Phase 1. Gate standard applied
throughout: can Phase 2 execute this table mechanically, with no re-deciding —
not whether the classifications are individually reasonable. Everything below
was independently re-measured, not re-read; every command's actual output is
pasted.

## (1) Completeness — measured against origin/main, not a local checkout

The trap Director named is real and I hit it: my own local `Skills` checkout
was 3 commits behind `origin/main` (`git status`: "Your branch is behind
'origin/main' by 3 commits"). Did not trust it. Listed the tree directly from
the ref instead:

```
$ cd ~/source/repos/Personal/Skills && git fetch origin --quiet
$ git log --oneline -1 origin/main
36b8206 feat: generate skills reconciliation and evidence-aware routing (#304)
```

Matches the table's cited SHA (`36b820661bc44bbeb66a1a0243757321932b17ac`)
exactly. Then, from that ref, no working-tree checkout involved:

```
$ git ls-tree -r --name-only origin/main -- docs | sort | wc -l
58
$ git ls-tree -r --name-only origin/main -- docs | grep -c '\.md$'
14
$ git ls-tree -r --name-only origin/main -- docs | grep -c '\.json$'
3
$ git ls-tree -r --name-only origin/main -- docs | grep -c '\.jsonl$'
41
$ git ls-tree -r --name-only origin/main -- docs | grep -vE '\.(md|json|jsonl)$'
(no output -- no other extensions exist)
```

**58 real files (14 md + 3 json + 41 jsonl), matching the table's own
stated method exactly.** Diffed both sub-lists against the table's rows:

```
$ diff <(git ls-tree -r --name-only origin/main -- docs | grep -E '\.(md|json)$' | sort) \
       <(grep -oP '(?<=\| `)docs/[^`]+(?=`)' run/p12-skills-disposition.md | sort -u)
(no output -- exact match, all 17 md/json files present, no phantom rows)

$ diff <(git ls-tree -r --name-only origin/main -- docs | grep '\.jsonl$' | xargs -n1 basename | sort) \
       <(grep -oP "(?<=\`)[a-zA-Z0-9_-]+\.jsonl(?=\`)" run/p12-skills-disposition.md | sort -u)
38d37
< test-in-the-consumer-context.jsonl
```

One real gap, non-blocking (see finding 1 below): `test-in-the-
consumer-context.jsonl`'s name is split across a hard line-wrap inside a
single backtick span (lines 118-119 of the table), so a naive extraction of
backtick-quoted `.jsonl` names misses it. The file is NOT omitted from the
table — it is correctly covered by the bulk `corpus` disposition and its
own consumer note (`tests/test_eval_status.py:649`) is present in prose —
only its individual name-as-written is not cleanly machine-extractable.
**Completeness: PASS**, all 58 files accounted for; one cosmetic rendering
defect flagged for a trivial fix, not a missing row.

## (2) Consumer checks re-run independently — 11 rows, every disposition class

Re-ran every grep myself, from a fresh detached worktree at the exact SHA
the table cites (`git worktree add --detach /tmp/skills-review-work
origin/main`), and a fresh detached worktree of agent-estate at the exact
SHA the table cites (`8ba75ac4e626e6872c69b333355fb7f1c16f0465`) — not the
locally-checked-out branches of either repo, which were behind or ahead of
those SHAs respectively.

**`docs/eval-status.json` (relocate-out-of-docs) — the highest-stakes row,
verified fully:**
```
$ sed -n '98p' scripts/eval_status.py
RECORD_PATH = REPO / "docs" / "eval-status.json"
$ sed -n '3p' .gitattributes
# docs/eval-status.json generating a merge conflict per PR, three times
$ sed -n '8,11p' /tmp/estate-review-work/src/tui/cmd/estate/skills.go
// skillsEvalStatusRelPath is where jonhill90/skills keeps skills#230's own
// eval-status.json, relative to that repo's root.
const skillsEvalStatusRelPath = "docs/eval-status.json"
```
Confirmed real: the estate TUI hardcodes this exact relative path as a Go
constant. Moving the file without a coordinated estate-side edit breaks the
skills-eval TUI pane, exactly as the table's own "What Phase 2 must not get
wrong" §1 states.

**`docs/skills-reconciliation.json` (relocate-out-of-docs, Push-4 generated
manifest) — constraint (5), verified fully, including a live re-run:**
```
$ grep -n "docs/skills-reconciliation.json" scripts/reconcile_skills.py README.md
scripts/reconcile_skills.py:277:    outputs={root/'docs/skills-reconciliation.json':...}
README.md:49:The [machine-readable manifest](docs/skills-reconciliation.json) names...
$ python3 scripts/reconcile_skills.py --check
Verified 41 public skill records; changed=0
```
The table's instruction is correct and matches this repo's own convention:
Phase 2 must re-run the generator and prove `--check` clean at the new
path, never hand-move the file. This is the constraint (5) Director named
explicitly, and the row satisfies it.

**`docs/skills-environment.json` (relocate-out-of-docs):**
```
$ grep -n "docs/skills-environment.json" scripts/reconcile_skills.py
observation=REPO/'docs/skills-environment.json'
```
Confirmed, matches the table's cite.

**`docs/eval-harness-findings.md` (historical, flagged as widely
cross-referenced):**
```
$ grep -n "eval-harness-findings" tests/test_skill_read_confirmed.py scripts/skill_read_confirmed.py
tests/test_skill_read_confirmed.py:192:  harness (docs/eval-harness-findings.md) -- confirm this function
scripts/skill_read_confirmed.py:10:  DIFFERENT already-known failure mode (`docs/eval-harness-findings.md`'s
scripts/skill_read_confirmed.py:71: `docs/eval-harness-findings.md`'s own "hand-scored, not a general harness
```
Both production-code hits are comment/docstring citations, not runtime
file-opens, exactly as the table characterizes them.

**`docs/eval-cost-axis-principle.md` (canonical, claimed zero
consumers):** `grep -rln "eval-cost-axis-principle" .` — zero hits.
Confirmed.

**`docs/eval-longitudinal-design.md` (research):** confirmed cited by
`docs/eval-harness-findings.md:252` and cites `docs/eval-harness-
adopt-or-build.md` outbound, as the table states — but the table's "is
cited BY" list is incomplete: `docs/eval-harness-adopt-or-build.md` itself
cites `eval-longitudinal-design.md` inbound 4 separate times (lines 75,
286, 327, 386), not listed. Non-blocking (finding 2 below) — the
disposition (research, untouched this phase) does not change.

**`docs/skills-docs-proposal-161.md` (research, claimed zero external
consumers):** confirmed, zero hits beyond its own outbound citation of
`docs/loop-tick-placement-160.md`.

**`docs/eval-log/tdd.jsonl` (corpus, the one jsonl with a claimed
file-specific hit):**
```
$ sed -n '645,649p' tests/test_eval_status.py
class TestLogDrift(EvalLogSandboxTestCase):
    """find_log_drift -- the guard agent-b3.md's PR comment on #245
    required: PR #245's own migration seeded docs/eval-log/ from a
    pre-#243 copy of docs/eval-status.json, so tdd.jsonl was never
    created. ...
```
Confirmed — narrates a past migration bug, not a live path dependency,
exactly as the table describes.

**`docs/eval-log/mechanize.jsonl` (corpus, spot-check of the "40 of 41,
no consumer" claim):** zero hits in skills, zero in agent-estate, zero in
the vault. Confirmed.

**DELETE-CANDIDATE ("none found") — independently re-verified, not just
re-read:**
```
$ find . -path ./.git -prune -o -type f \( -name "*.md" -o -name "*.py" \) -print \
    | xargs sha256sum | awk '{print $1}' | sort | uniq -d | wc -l
0
```
Zero duplicate checksums across every tracked `.md`/`.py` file. Confirms
the table's claim independently, not merely repeats it.

**Third-party skills ("none found") — independently re-verified:**
```
$ python3 -c "import json; d=json.load(open('docs/skills-reconciliation.json'));
print(set(s.get('authorship',{}).get('class') for s in d['skills']))"
{'jon-or-agent-attributed'}
$ for f in $(find skills -name SKILL.md | head -6); do
    echo -n "$f: "; git log --reverse --diff-filter=A --format=%an -- "$f" | head -1
  done
skills/github-cli/SKILL.md: Jon Hill
skills/decide-by-variant/SKILL.md: Jon Hill
skills/obsidian/SKILL.md: Jon Hill
skills/sanity-check/SKILL.md: Jon Hill
skills/determine-intent/SKILL.md: Jon Hill
skills/tdd/SKILL.md: Jon Hill
```
All 41 `SKILL.md` files present (`find skills -name SKILL.md | wc -l` → 41),
sample of 6 add-commits all authored by Jon Hill, authorship field 100%
`jon-or-agent-attributed`. Confirms "none found" independently.

**SKILLS-INDEX.md dead — independently re-verified:**
```
$ git log --oneline -- SKILLS-INDEX.md
789dd74 docs: delete SKILLS-INDEX.md -- stale-copy anti-pattern (#303)
c8ad9ba docs: index available skills (#302)
```
Confirmed deleted by #303 for exactly the reason the table states.

11 rows checked, spanning canonical, historical, research, corpus (2
instances), relocate-out-of-docs (all 3), DELETE-CANDIDATE (the whole
class), and third-party (the whole class). Every substantive factual claim
I re-derived matched. **Consumer checks: PASS**, with two non-blocking
precision findings below.

## (3) Public-repo leak check

```
$ grep -n "private\|Private" run/p12-skills-disposition.md
14:**Repo is PUBLIC.** No private skill name, path, or content appears
17:Counts only where a private surface would otherwise be implied (none
18:were; this repo has no private skills of its own -- see the third-party /
```
No private skill name, path, or content appears anywhere in the table. The
only other appearance of the word "private" is a direct quote from
`scripts/eval_status.py`'s own public docstring naming the private harness
*repository* (`agent-evals`) by category, which is already how that public
script describes itself — not a leak of any private skill's identity or
content. Every citation in the table is either a public `skills/` path
already in this same public repo, or a public agent-estate path. **Leak
check: PASS.**

## (4) SKILLS-INDEX.md — confirmed dead, nothing proposed to resurrect it

Verified independently above (git log). The table's own §"SKILLS-INDEX.md"
section states plainly that nothing here proposes reviving it under any
name, and I read the rest of the table for any routing-surface proposal
that might smuggle it back in under new dress — none exists. The only
routing-adjacent content mentioned is `README.md`'s already-generated,
link-only render string (`reconcile_skills.py`'s own `render()` function,
confirmed above), which is exactly the GENERATED-from-`SKILL.md`-frontmatter,
link-only shape constraint (4) requires — the table proposes no NEW
routing surface at all in Phase 1 (correctly; that is Phase 3's job per
the execution plan). **PASS.**

## (5) Push-4 generated manifest — byte-for-byte re-run instruction present

Verified above: `docs/skills-reconciliation.json`'s row explicitly
instructs Phase 2 to re-run the generator and prove `--check` clean at the
new location, "never hand-move the file," and separately calls out that
the generator's own hardcoded output path and the README render string
both need the same-commit update. This is precisely what constraint (5)
requires, and I independently reproduced the exact `--check` output the
table cites. **PASS.**

## (6) Third-party skills — a list, never scheduled for execution

Verified above: the finding is "none found," stated as a finding, not a
list with anything marked for deletion or any other action. Nothing in
this section or elsewhere in the table schedules a third-party skill for
removal. **PASS** (vacuously — there is nothing to accidentally schedule,
and the table does not pretend otherwise).

## (7) Zero moves; README.md/AGENTS.md untouched

```
$ cd ~/source/repos/Personal/Skills && git status --short
?? AGENTS.LOCAL.md
```
The only untracked item in the local checkout is `AGENTS.LOCAL.md` — a
personal, pre-existing local file, not part of `docs/`, not touched by
this pass, not referenced anywhere in the table. No modified files, no
staged files, no moved files. `README.md` and `AGENTS.md` are both absent
from this diff — neither was touched. The table's own claim of a reverted
throwaway local branch ("git mv's on a throwaway local branch... never
committed or pushed, branch deleted") could not be independently verified
by definition (a deleted, never-pushed local branch leaves no discoverable
trace) — but `git branch -a` shows no branch matching that description
still present, and `origin/main` matches the table's cited SHA exactly,
which is the part of the claim that actually matters for this gate.
**PASS.**

## Findings (both non-blocking — neither changes a Phase 2 mechanical
action, neither is a missing or phantom row)

1. **`test-in-the-consumer-context.jsonl`'s name is split across a hard
   line-wrap inside a single backtick span** (table lines 118-119),
   making it mis-extractable by naive tooling even though its disposition
   (bulk `corpus`) and its individual consumer note are both correctly
   present in prose. Trivial fix: join the span onto one line.
2. **`docs/eval-longitudinal-design.md`'s "is cited BY" list is
   incomplete** — omits `docs/eval-harness-adopt-or-build.md`'s own 4
   inbound citations of it. Does not change the row's `research`
   disposition or any Phase 2 action, since research rows are not moved
   this phase.

Neither finding blocks Phase 2 from executing this table mechanically and
correctly. Every row I independently re-derived — spanning all six
disposition classes plus the two "none found" classes — matched the
table's claim. The two highest-risk rows (`docs/eval-status.json`'s
cross-repo Go-constant dependency and `docs/skills-reconciliation.json`'s
generator-reproducibility requirement) are both real, both correctly
identified, and both correctly instructed for Phase 2.
