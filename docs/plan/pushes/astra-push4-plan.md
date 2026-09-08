# Astra one-shot — Push 4: the skills pillar (K5)

ONE-SHOT BUILD: branches + PRs only, never merge, never push to main.
Decisions are made — do not relitigate; smallest reading on ambiguity,
noted in the PR body. Read first, in order:
1. `push4-skills-parameters.md` — THE LAW for this push (460 lines,
   updated post-review): Jon's recorded skill parameters, item-id cited.
   Where this brief and that document conflict, that document wins.
   WEIGHTS BIND DIFFERENTLY: each entry carries its corpus weight —
   `hard` entries are binding constraints; `preference` entries (incl.
   the npx-install, harness-agnostic, evals-or-gap, measured-roster
   items) are stated preferences to honour — do work guided by them,
   but never refuse/hard-fail on their account, and report findings
   against them as preference deviations, not law violations. An id you
   cannot find in the law doc: query the corpus directly
   (`sqlite3 -readonly ~/corpus/corpus.sqlite3 "select body,weight from
   items where id like 'it-<prefix>%'"`) — never conclude 'no such
   law' from a failed grep; ids in this brief are prefixes of the
   16-char corpus form.
2. `master-execution-plan.md` §Push 4 + §Repo scope.
3. `iteration-queue.md` (P7 done; P11 belongs to the LANES, not you).

FIRST ACTION: verify and record current state in your report — compare
against ORIGIN/MAIN explicitly (a behind local checkout generated a
false report tonight): origin/main sha of jonhill90/skills (P7 merged at
`789dd7433` — verify, don't trust) and agent-estate,
`ls ~/.claude/skills | wc -l`, skills repo top-level. Do not trust this
brief's numbers.

Repo scope (binding): jonhill90/skills, skills-private (only if work-only
content must move there), agent-estate (catalogue registration ONLY).
Do NOT touch agent-dotfiles — a lane is actively working it (A3/P11);
anything you find that needs a dotfiles change goes in your report as a
handoff item, not a commit. No Hill90. No Second Brain. No evals RUN.

## B1 — Inventory truth: every skill, exactly one home

Enumerate three surfaces: jonhill90/skills tree, skills-private tree
(read-only unless B2 requires), `~/.claude/skills` installed set. Produce
a reconciliation manifest (in the skills repo, generated-not-hand-kept):
per skill — name, canonical home (exactly one), installed-where,
authored-by-Jon vs third-party, scoping class per the law doc
(public / project / work-only / personal-only), and any COPY found in
more than one place. Third-party skills committed into Jon's repos
violate `it-957502d135216f90` — list them; do not delete anything
(deletions are a list for Jon, always). The manifest's GENERATOR must be
committed, rerunnable, and reproduce its committed output byte-for-byte
(P7's own acceptance — otherwise you are rebuilding SKILLS-INDEX.md
under a new name).

## B2 — One-home enforcement, mechanically safe

For every duplicate/vendored finding in B1 with an unambiguous fix
(e.g. a Jon-authored skill existing in two repos): converge to the one
canonical home, leaving no copies — REMOVING A VERIFIED BYTE-IDENTICAL
DUPLICATE of a Jon-authored skill does NOT count as deletion, PROVIDED
the canonical copy is proven identical first (sha256 both, paste the
hashes); any non-identical or ambiguous copy stays and goes on the
Jon-list. The INSTALL path is the mechanism for
presence elsewhere (`npx skills add` at onboarding — `it-c10defd3`,
`it-33e6ad272`: progressive, per-job, never baked in). Anything
ambiguous (unclear authorship, work-vs-personal, employer-content
suspicion per `it-850afa9e`/`it-9e769f3d`) goes on the Jon-list with
your best-guess disposition stated. No plugins introduced
(`it-bca91f79`).

## B3 — Harness-agnostic packaging audit

Per the law (`it-ebe6f2ee`, `it-1fd291f8`): skills must work on web
harnesses where they are UPLOADED, not read from disk. Audit each
Jon-authored skill's shape: does it depend on local paths, repo
checkouts, or CLI-only assumptions that break when uploaded? Report
per-skill PASS / FAIL-with-reason. FIX only mechanical, zero-judgement
cases (e.g. a hardcoded absolute path with an obvious relative form);
everything else is findings, not changes.

## B4 — Eval-status honesty, no eval-building

Per `it-377e6dbc`: a published skill carries evals, or the gap is named.
For each skill in the manifest: eval status = has-evals (where/what) |
no-evals (named gap). GENERATED into the routing surface from real
inspection — never invented. could-not-measure IS a status value and
appears IN the generated surface (an honest gap shown beats a silent
one — the exact failure the evals-or-gap preference exists to prevent),
with detail in the report. Do NOT build eval infrastructure, do NOT run evals
(`it-f61bb8e5`'s measured-roster rule is Push 4 step 3, AFTER this
inventory — and the methodology home is agent-evals, not the skills
repo). Peter-shareability (`it-9cc619d4`) is a scoping-class note, not a
sharing action.

## B5 — Register and route

Register the reconciled skills repo state in the estate catalogue
(existing verbs; ID-stability rule binds: identityFor = sha256(Locator)
ALONE — acceptance is the push-3 proof, not an assertion: list every
existing 05 - Sources record id before and after, show unchanged).
Vault writes: through the reviewed tool only, and IF any lane is
actively writing the vault when you get there, pointer updates become a
HANDOFF ITEM in your report, not a commit — same fence as dotfiles. The repo's README/docs index (P7's
no-copies pattern — generated from SKILL.md frontmatter, link-only)
absorbs the manifest's human-facing view; SKILLS-INDEX.md stays dead.

## Constraints (binding)

- Nothing installed en masse; nothing evaluated; nothing deleted (lists
  for Jon instead). Employer/work content NEVER moves into public repos —
  suspicion = flag, not move (`it-850afa9e`, `it-9e769f3d`).
- skills repo is PUBLIC: no private skill names/content beyond counts
  (existing P7 rule).
- Every claim in reports carries real command output; could-not-measure
  is a verdict. Quote Jon only via the law doc's already-clean text.
- W1-style discipline for any generated-file churn: backups, batch,
  verify.

## Definition of done

PRs in jonhill90/skills (+ estate if B5 needs code) with green checks,
each body: what/evidence/not-done/attack-first. Final report
`run/astra-push4-report.md`: per-workstream status, the Jon-list
(ambiguous dispositions + deletion candidates), dotfiles handoff items,
honest FAILs/UNRUNs, resume locations. Worktrees preserved.
