# Master execution plan — the estate's knowledge architecture, complete
2026-09-07 ~00:00 EDT. Written by Fable with Jon after the big-picture
review. Supersedes nothing; sequences everything. One push at a time; each
push has a gate; no push starts until the prior push's gate is REPORTED
(pass or fail, honestly).

## The picture (agreed vocabulary)

ESTATE = the product. Subsystems: ORCHESTRATION (the supervisor — engine),
KNOWLEDGE SYSTEM (machine-wide: serves estate AND dotfiles), TUI, COMMS,
COST, EVALS(adjacent repo). Knowledge pillars: Agent Memory (INMAPS vault),
Corpus, Skills, Repo docs, Retrieval/gates. (UI rendering: OUT OF SCOPE — no Hill90 work in this effort; Jon 2026-09-07.)
Layers: 1 evidence (transcripts/corpus/ledger) · 2 knowledge (vault md,
OKF 0.2) · 3 views (indexes/MOCs/compiled index — regenerable).
Pending Jon confirmation, then it becomes law: **md is sufficient to
operate; sqlite is the evidence archive** (implies vault under git too).

## Repo scope (Jon, 2026-09-07 00:30 — binding)

agent-estate, agent-dotfiles, skills, skills-private (if needed),
agent-evals. NOTHING else — the Hill90 family is out; its trackers are
not inputs to this effort.

## Standing crew model

- Jon: talks, judges, approves pushes. Intent decisions only.
- Fable: plan authorship, hard reviews, unblocking judgement. Reserved.
- Astra (1-shot, quota-gated): big builds from a hardened brief; branches
  + PRs only, never merges.
- Terra (director:2, quota-gated): sparse strategic supervision of
  director:1 via short pointer prompts.
- Director:1 (opus): run management, dispatch, merges under protocol.
  KNOWN DEFECT: turn-continuity (announces without acting; polls late).
  Interim: Fable's flow-watcher auto-nudge. Real fix in Push 5.
- Sonnet lanes (agent-estate:1-3, cdsp --model sonnet --effort medium):
  build, fix, review. Fixer ≠ reviewer ≠ author, always.
- Merge protocol: cross-lane APPROVE at exact head + green checks;
  director merges; one fix pass; second failure closes the PR.

## Push 3 — finish the memory foundation (NOW → tomorrow)

Goal: the vault is the complete, self-sufficient Agent Memory; agent/ gone.
1. P5 remainder: dissolve agent/ per spec §7b. Big batch = parameters:
   1,104 corpus items → individual notes, letter subdir (01p-style),
   vault-view repointed to regenerate INTO the structure, consumers
   updated in the same change. Batched, validated, reviewed.
2. P6: ledger name collision (corpus.sqlite3 + compat symlink + Path()
   repoint + 99 - Meta disambiguation note).
3. P3: worktree/branch cleanup (39 worktrees, 391 branches; preserve
   list in iteration-queue P3; avoid #1247's broken sweep).
4. If Jon confirms the md-sufficiency parameter: put the vault under git
   (private repo), iCloud stays as sync not backup-of-record.
GATE: agent/ contains nothing; validator green; a fresh worker completes
an ordinary task from vault routing alone; parameters filterable by tag
in Obsidian. Report with command output.

## Push 4 — the skills pillar (K5 proper)

Goal: the skills system obeys Jon's ~12 recorded skill parameters.
Inputs: the parameter notes from Push 3 (tag: skills), W5's index,
jonhill90/skills repo, ~/.claude/skills live set.
1. Compile the skills parameter set from the corpus into K5's planning
   input (one doc, every parameter cited by item id).
2. Reconcile reality against law: one-home-per-skill (repo pulls at
   onboarding — kill copies), scoping axis (public/project/work/personal;
   employer content OUT), progressive install via npx skills, no plugins,
   harness-agnostic packaging (web-uploadable), eval status honest per
   skill (carries evals or names the gap).
3. Evaluation loop design only AFTER inventory reconciles — measured
   roster decisions, not intuition (Jon's own rule binds Jon too).
Astra one-shot candidate: the mechanical reconciliation (moves, manifest,
index regen). Fable hardens the brief first; director+lanes iterate.
GATE: every installed skill traces to exactly one canonical home; scoping
labeled; eval status stated; zero employer content in personal repos.

## Push 5 — K6 + supervisor drive-continuity (the systems meet)

Goal: a fresh/stateless worker demonstrably continues real work using the
knowledge layer — the original point of everything.
1. Land/finish #1223 (persistent stream-json worker, K6 slice 1).
2. Run K6 proper: stateful tmux lane vs fresh dispatch on the same task
   with a cited resume packet (#1187), measured not vibed.
3. Supervisor hardening from tonight's measured defects: director
   heartbeat (tick-driven turns, the CLI loop pattern applied to the
   tmux director), flow-watch mechanized into the estate (replacing
   Fable's session-bound script), #1248 watcher re-arm, #1247 sweep fix,
   #1224 delegation record, #1254 law-separation pin.
GATE: K6 comparison produces a number Jon can read; the estate runs one
full unattended evening with ZERO human flow interventions.

## Push 6 — machine-wide closure

1. Dotfiles: finish K4 remainder (docs marked canonical/historical fully,
   deletable list to Jon), AGENTS.md routing verified by a stranger test.
2. Corpus → OKF start (Jon's parameter: after memory works): vault-view
   retired in favor of the parameter notes; corpus export design doc.
3. Knowledge CLI as PATH binary decision (any repo queries without an
   estate checkout).
GATE: an agent in dotfiles (not estate) answers a knowledge question with
citations using only routing + the PATH tool.

## Later, explicitly not now

Graph/vector views over stable IDs (Graph Engineering). Per-agent memory format (Jon's
call). Second Brain: never touched. Corpus full OKF landing. TUI knowledge
panes.

## Decision points reserved to Jon (asked at the right moment, not now)

1. Confirm: "md sufficient to operate, sqlite is evidence" + vault in git.
2. Letter scheme for Notes subdirs (01p vs 01np vs his 01nf pattern).
3. Skills evaluation bar (what "evaluated" requires) — at Push 4 step 3.
4. K6 verdict thresholds — what number convinces him.
5. (removed — Hill90 is out of scope entirely; Jon 2026-09-07)

## Cadence

Each push: Fable writes/hardens the brief (with director adversarial
review — tonight's 15-finding pattern is now standard), Astra one-shots
the mechanical bulk when quota allows, director+lanes iterate and merge,
gate reported with evidence, Jon inspects in Obsidian/GitHub. Corrections
fold into the next brief, not into mid-flight scope.

## Issue-tracker reconciliation (added 00:15 after full 3-repo sweep)

The pushes above hold, but the tracker adds/corrects:

**Push 3 additions:** #330 (corpus lacks a STANDING RULE category — fold
into the parameter-notes type taxonomy while migrating); #1084/#1089 (Jon
has NO surface to read his 2,638 hard items — P5's parameter notes ARE
that surface; close both against it when it lands); #705 (684 prompts
captured but unjudged — the notes-update-in-place loop needs the judging
backlog drained or new notes lag his words).

**Push 4 additions:** #1021 (metadata-only registry of external skills)
may be substantially closable against W5's index — verify, don't assume.
dotfiles #6 (non-authored skills policy — parked, revisit with the
scoping axis). #453 (mine Paperclip/Gastown) stays backlog.

**Push 5 corrections — two under-scoped families:**
(a) K1 RETRIEVAL-QUALITY DEBT is ~25 open issues (goldenquery ratchet
    unrun #1210/#1208, disclosure untested #1202/#1200/#1170, scoring/
    tuning #1135/#1113/#1105/#1063, index guards #1191/#1193/#1123,
    grounding adoption #1159/#1099/#1053...). This was not a named
    workstream. It becomes Push 5's third leg: pick the ~6 with teeth
    (ratchet-in-CI, disclosure tests, adoption measurement #1103),
    close-or-defer the rest EXPLICITLY.
(b) SUPERVISOR family is ~30 issues, not the 6 named (add at least:
    #1029 silent lane death, #1004 harness-failure detection, #993
    session-quota unwired, #1163 quota hang, #680/#574 completed_at,
    #1194 mid-turn strand, #980 merge naming, #787 session move).
    Push 5 picks by tonight's evidence: flow/liveness first.

**Push 6 additions:** dotfiles #323 (docs staleness across four repos —
the drift-detection registration is the fix), #272 (memory backend:
neither forgetful nor cognee; its own finding — "our failure is
consultation, not retrieval" — VALIDATES the read-protocol work; keep
parked), #52 (notification architecture), #16/#322/#57 (supervisor/
hierarchy design — Jon-flavored, surface at Push 6 for sequencing).

**Standing debt not in any push, named so it isn't lost:** #682's
irreversible migration steps queued for Jon; #1013 (herdr ideas);
#284 (transport migration remnant); #439/438/437 (docs/diagram/council
backlog); #591 (19 lane branches with unpushed commits — fold into P3
cleanup's preserve-check); #601/595/594 (workflow/verdict hygiene).

Rule going forward: each push's brief links the exact issues it claims,
and closing an issue requires its own acceptance test, not proximity.

## Later-item added (Jon, 2026-09-07 ~00:20): synaptic reinforcement

Track last_viewed + view_count per note (OKF already reserves
usage_count/usage_window — the slot exists; instrument estate knowledge
query/get to record reads). Then, separately decided: let view-strength
influence retrieval weight and graph edge weight — "stronger connections
the more views," Jon's synaptic model. Check graphify's prior art before
building. NOT in Pushes 3-6; revisit when graph views come up. Frontmatter
designed tonight is already compatible — no rework needed later.

## Phase insert (Jon's catch, 2026-09-07 ~02:50): LINKING

Associative note-to-note linking currently has NO owner — provenance,
hub-spoke, and legacy fact wikilinks exist; the synaptic web does not
(corpus twin: #1095, relation model zero rows). Insert as Push 4.5 (may
run alongside Push 4's skills work — different surfaces): a relation
PROPOSER (shared sources/tags/textual reference/contradiction candidates
→ typed relations per Jon's INMPARA taxonomy: relates_to, requires,
enables, ...) flowing through the existing review pipeline; accepted
relations written to frontmatter/body by the tool. Prerequisite for all
graph work; substrate for reinforcement edge-weights. Gate: relations
exist between notes that share no hub, proposed-not-imposed, and the
Obsidian graph shows cross-links, not just spokes.

Addendum (Jon, same conversation): TAGS are the other half of Push 4.5.
The standard/vocabulary/validation exist; missing: (a) the associative
tagging pass over the 2,638 corpus notes (agent judgment, tag
associatively per Jon's golden rule, vocabulary-governed, batched via
tool — no Jon approval per his parameter); (b) tags wired into retrieval
(estate knowledge query filter/boost by tag) so they are load-bearing,
not decorative — the measured Second Brain failure mode; (c) topic-MOC
birth then fires naturally from the new tag density. Gate extends: a
tag-filter question ("show me the azure parameters") answers correctly
in BOTH Obsidian and estate knowledge query.

### Push 4.5 — Director pre-check, measured 2026-09-07 (do not rediscover this)

Measured against `main` at the time of writing. Two findings shape the brief.

**1. The tag field the associative pass needs ALREADY EXISTS — and vault notes
cannot reach it.** `internal/knowledge/knowledge.go:79-83` defines
`SynapticTags` with the doc comment "SynapticTags are #hashtag, associative --
lifted mechanically from …", and `bm25.go:111` already folds both
`StructuralTags` and `SynapticTags` into the searchable text. So retrieval
*plumbing* for associative tags is built and load-bearing today.

**But the only populator is `stars.go:82` (`SynapticTags: hashtag(r.Topics)`)
— the GitHub-stars source.** `internal/knowledge/vault.go` has **zero** tag
handling: grepping it for `tags` returns nothing. Vault-note frontmatter `tags:`
never reaches the index.

**Consequence for the gate**: the associative tagging pass could tag all 2,638
corpus notes correctly, the vault could validate clean, Obsidian could answer
"show me the azure parameters" perfectly — and `estate knowledge query` would
still return nothing, because the tags never enter the index. That is the P9
failure class again (content written where the reader does not look), and it
would present as "tags are decorative", which is the exact Second Brain failure
mode the addendum says to avoid.

**Required in the same change as the tagging pass:** teach `vault.go` to read
note frontmatter `tags:` into `SynapticTags` (the field is already there and
already searched — this is wiring, not new design), and prove it with a query
that returns a note found ONLY by its tag. Do this BEFORE or WITH the pass over
2,638 notes, never after — otherwise the pass is unverifiable while it runs.

**2. `query.go` already reads `SynapticTags` at two call sites** (lines ~683
and ~795), so filter/boost-by-tag may be partly present rather than absent.
Whoever takes this should establish what `query` does with tags TODAY —
filter? boost? searchable-text only? — and state it, rather than assuming the
"wire tags into retrieval" work starts from zero. The honest answer may be
"the plumbing exists, only the vault populator is missing", which is a much
smaller job than the plan implies.

Not checked here (out of scope for a pre-check, flagged for the brief):
`#1095`'s relation model and whether the corpus relation table is genuinely
zero-rows or merely unpopulated by the current writer.

## Discipline tracks (Jon, 2026-09-07 ~03:05): Loop Engineering → Graph Engineering

Jon's two coined disciplines, sequenced against the pushes:

**Loop Engineering — resumes at Push 5.** The supervisor hardening IS the
applied work: director heartbeat/tick-driven turns, mechanized flow-watch,
watcher re-arm, K6's persistent worker, the unattended-evening gate.
Evidence file opens with tonight's three measured director defects (poll
gap, announce-without-dispatch, tick-masked stall). Research home:
Loops-Research (exists; registered as a catalogue source).

**Graph Engineering — opens at the Push 5→6 boundary.** The successor
discipline: composing RELIABLE loops into explicit graphs — the estate's
own orchestration (director → lanes → reviews → merges, K6 pipeline)
modeled as nodes/edges. NOT knowledge graphs (separate, parked). Gated on
Push 5 because unreliable loops cannot be composed. Deliverables at open:
1. Create `~/source/repos/Personal/GraphEngineering-Research` (named in
   Jon's prompts, never created — currently an unstarted deliverable) and
   register it as a source.
2. Digest Jon's four linked articles (langchain 3-years-of-graph-
   engineering-with-langgraph, theaioperator what-is-graph-engineering,
   intuitionmachine from-loop-to-graph, aibuilderclub guide) + newer
   material; write the research doc.
3. Answer Jon's standing question: does his closing-the-loop skill amount
   to Graph Engineering? Compare vs Obra Superpowers / Matt Pocock skills.
4. Put the REBRAND decision in front of Jon with real options ("we may
   need to rebrand from loop engineering to graph engineering, since some
   of this applies directly to the tool we are building") — his call,
   made with the estate's flow actually drawn as a graph in hand.

Sequencing note: the knowledge architecture (Pushes 1-4.5) is the
prerequisite substrate for both — loops that consult memory, graph nodes
that cite what they know.

## The Estate product arc (added 03:10 — where the product itself lands)

Every push above IS estate work — the product being built is the estate;
knowledge and loops are its subsystems. But Jon's recorded roadmap
wishlist (corpus: Memory, Shared Memory, Knowledge, Skills, MCP,
Connectors, workflows, director-over-supervisor loops, cost management,
knowledge graph views — "keep iterating even if imperfect") extends past
Push 6. The product arc after the discipline tracks open:

1. **TUI catches up to the subsystems** — knowledge panes (query, vault
   browse, MOC views), workflows view fed by the ledger, the graph view
   rendered from Push 4.5's edges. The TUI is the estate's face; it
   deliberately waited until there was something true to show.
2. **Personas / per-agent memory** (Jon's reserved decision) — the
   03 - Agents seat gets filled: domain experts scoped by the knowledge
   layer, format chosen by Jon (vector/OKF/graph still open).
3. **MCP + Connectors survey** — what the estate exposes and consumes;
   includes forcing harness-native memory to the shared layer (parked
   until shared memory proved solid — that proof is Push 3-5's gates).
4. **Cost management maturity** — spend/quota beyond gating: per-push
   cost reporting like tonight's (Astra 87%→13% for Push 3's build),
   budget-aware dispatch.
5. **Delivery framing** (Jon's standing rule): each product milestone
   reports "what a human can now do that they could not before" — never
   merely "merged."

Sequencing: opens as Push 7+ after Graph Engineering's research lands,
EXCEPT TUI knowledge panes which may start any time after Push 6 (their
data contracts stabilize there). The estate is the product; this section
exists so "the product" never again reads as missing from its own plan.

## Completeness audit (03:20 — final sweep, three real gaps found + closed)

Verified covered: all six knowledge pillars, both discipline tracks, the
product arc, K-gates, issue reconciliation (127 across 3 repos), Jon's
pending confirms, deferrals, Second Brain boundary, Hill90 exclusion,
crew/quota model, merge protocol, tag/link phases, reinforcement parking.

Gaps found by this audit, now owned:

1. **EVALS TRACK had no scheduled item.** agent-evals has its own open
   work (harbor as eval runner #20, verdict redesign #3) and Jon's law
   says published skills carry evals — but nothing scheduled the eval
   methodology work. Added: Push 4 step 3 (skills eval bar) explicitly
   HANDS OFF to agent-evals as the methodology home; the harbor-runner
   decision surfaces to Jon there. Not a new push — a named handoff.
2. **EVIDENCE-LAYER BACKUP has no owner.** The md-sufficiency law makes
   the vault survivable, but corpus.sqlite3 (25MB, ~/corpus/, one copy +
   a backups/ dir of unknown discipline) and the estate ledger are the
   receipts — losing them loses provenance. Added to Push 3 tail: a
   boring, verified backup routine for ~/corpus and
   ~/.local/state/estate (schedule + restore test + where-it-goes), and
   the vault-in-git decision (Jon's confirm #1) covers layer 2.
3. **HOUSEKEEPING loose ends from tonight, so they don't fossilize:**
   Fable's stash on the main checkout ('fable: tick-log...' — restore or
   drop after Push 3 gate), two older WIP stashes of unknown value
   (inspect before dropping — safe-deletion rule), untracked
   src/progress/progress binary, and the 14 recommended-metadata
   validator warnings (fold into any P9/P10 follow-up lane pass).

Explicitly checked and NOT gaps: Notebook-MCP (reference source only),
launchd plists #529 (tracker, Push 5 hygiene family), Peter-sharing
(Push 4 scoping axis), work-Mac copilot evals (Jon's own task, tracker),
TUI merge-path duality (tracker), session continuity (run/ directory IS
the handoff).
