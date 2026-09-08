# Lane A — visible workspace and canonical guidance

Read `run/execution-plan.md` (same directory as this brief) first; it holds
ownership, sequence, review/merge protocol, and constraints. You are Lane A.
Your worktree: `~/source/repos/Personal/agent-estate-lanes/lane-a`, branch
`knowledge/lane-a`. The Agent Memory vault:
`$AGENT_MEMORY_VAULT` (= `~/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory`).
You are the ONLY writer of vault files during this run.

## Deliverable 1 — vault navigation, inspectable fast (do this first)

1. Back up every vault file you will touch to
   `~/source/repos/Personal/agent-estate-lanes/run/vault-backup/` preserving
   relative paths, and write `sha256` checksums to `checksums.txt` there,
   BEFORE any edit.
2. Create at the vault ROOT (additive only — move nothing, copy no fact
   bodies):
   - `Start Here.md` — the front page: what this vault is, one link per
     numbered area, link to `agent/index.md`.
   - `00 - Inbox/index.md`, `01 - Sources/index.md`, `02 - Projects/index.md`,
     `02 - Projects/agent-estate.md` (status: active; links to repo canonical
     docs), `03 - Shared Memory/index.md` (links to `agent/index.md` and
     current facts — links, never copies), `04 - Skills and Tools/index.md`
     (inventory of existing relevant skills/tools: name, purpose, canonical
     location, evaluated/unassessed), `05 - System/index.md`,
     `05 - System/tags.md`.
   - Make `agent/00 - Inbox/README.md` a signpost to the root `00 - Inbox/`.
3. `05 - System/tags.md`: a small governed vocabulary — source kind,
   lifecycle state, project, topic — with its extension rule (agents may
   extend through this file via PR'd change; routine tagging needs no Jon
   approval). No MOCs this run.
4. Correct vault `agent/ROUTING.md` and `agent/LIFECYCLE.md` command
   examples against the REAL CLI (`go run ./src/estate` from a repo
   checkout). Known wrong: `estate vault-view` does not exist — find the
   true regeneration path or mark the row honestly as "no current command".
   Verify every command you write by running it; paste output in your PR.
5. New managed Markdown follows OKF 0.2
   (`github.com/GoogleCloudPlatform/open-knowledge-format/blob/main/SPEC.md`;
   context in `~/source/repos/Personal/agent-dotfiles/docs/okf-0.2-study-2026-08-23.md`).
   Validate what you create; report (don't rewrite) legacy 0.1 issues.
6. Run the vault's own validator (`tools/validate_index.py` under
   `$AGENT_MEMORY_VAULT/agent/`) after your changes; paste output.

## Deliverable 2 — the contract (unblocks B and C; within ~30 min)

Write `run/contract.md`: the frontmatter fields a source record carries
(stable id, locator, kind, provenance/attribution, authority, access,
revision/hash, observed-at, freshness, review state, derivative links), the
tag vocabulary reference, and the canonical-destination rule (repo behavior
beside code; project decisions in the project; procedures in guides/skills;
cross-project preferences in Agent Memory; other surfaces link).

## Deliverable 3 — repo guidance (your first PR, then a final PR)

In your worktree: `docs/knowledge-workflow.md` (short canonical usage guide:
register → inspect → propose → review → publish/link → retrieve → refresh,
with real commands), one routing line in AGENTS.md linking it, a
repo-local skill `.claude/skills/knowledge-session/SKILL.md` that REFERENCES
the guide (never copies it), and `docs/decisions/` entry recording the
scoped one-push merge exception. First PR = vault work evidence + contract +
decision doc; final PR (after B and C merge) = guide + skill, updated to
match the CLI as actually merged.

## Evidence bar

Screenshots are not required; working links are. Every claim of "command
works" carries its actual output. Your PR body: `Author-Lane:` per the plan.
You review Lane C's PR (verdict comment per protocol). If something you need
does not exist, say so in the PR — do not invent it. Write
`run/handoff-lane-a.md` before the 19:15 EDT checkpoint: done, not-done,
next steps a cold session could resume.
