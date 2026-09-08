# Director:1 — authorized execution handoff from Jon via Astra

2026-09-07, after the Claude update. Jon approved sending you this handoff and updating Fable. You remain the single run manager; the three existing estate lanes remain your crew. Execute [EXECUTION-PLAN.md](EXECUTION-PLAN.md), with the current-state corrections below, and integrate its linked knowledge PRD/SPEC with the existing documentation work.

## I read your latest report before sending this

Your report says the update removed `~/.claude/jobs/8182f39f/tmp/`, including the watcher and two recovery copies; primary work survived; you rebuilt the watcher at `run/tools/healthtick.sh`; and the three worker lanes are fresh sessions with no memory of today's work.

Astra independently confirmed: the old `dotfiles-at-risk` backup directory is absent; the new watcher script exists (its runtime behavior was not independently tested); the dotfiles working tree still has the previously observed modified lock/guard and untracked work; remote estate main is now `1b581de4b01a507bb860475d9eab6905c8743988`; and dotfiles #348 is OPEN/MERGEABLE at `f912993c91f6a56e80f989cf435ba25c0897c5ba` with its repository check successful. These supersede older snapshots in the packet. Do not inherit the previous lane sessions' context or review identity from window names.

## Execute in this order

1. Before any live mutation, replace the vanished recovery copies from inspected surviving originals in a durable private location outside harness scratch. Record contents/hashes and verify restore into a disposable location. Preserve uncommitted/untracked work and the unpushed dotfiles commit. Report any evidence that cannot be recovered; do not claim nothing was lost merely because some originals survive. Do not delete backups or “clean up” that checkout.
2. Finish current integration: assign an independent review of #348 at its exact current head, with a self-contained brief that names scope, checkout, author identity, review rules and commands. A fresh session does not erase prior authorship. Merge only under your already-applicable review/merge authorization. P12 estate #1285 has landed; do not repeat its implementation. Verify any changed PR state once at assignment.
3. Read the recovery plan and its linked PRD/SPEC/evidence. Assign the first projection-repair slice to an available existing lane with one independent reviewer. Briefs must carry full necessary context, current base, file ownership, semantic contracts and tests because sessions are fresh. Preserve any still-active assignment.
4. Follow the connected workflow through current private retrieval, source-backed publication, and behavioral verification. The plan's later index rebuild and dual corpus/vault applicability tests are required. Produce actual before/after notes and command evidence; never infer usefulness from counts or tool activity. Keep unresolved editorial and re-triage totals visible.

Fable is an active advisor/council member: ask it for bounded technical judgment and adversarial review where needed. It is not a competing director or implementation lane and must not independently dispatch, merge, migrate the vault, or redesign the run. It is receiving the same context separately.

Protect Jon's Codex reserve: do not request additional Astra/subagent work without Jon's approval. Existing Claude lanes implement and review; Astra is reserved for later independent sanity checks. Use short changed-state reports and durable pointer briefs, not repeated history dumps or a new council cycle.

This handoff does not authorize deleting anything, rewriting the protected shared knowledge index, touching Second Brain/other product families, modifying credentials, or inventing new storage policy. Execute within the plan and existing authorization; report genuine unresolved authority precisely. Acknowledge the post-update recovery status and name the first actual lane assignment, then continue work.
