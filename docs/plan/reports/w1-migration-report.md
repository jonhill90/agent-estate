# W1 migration evidence

Validator regression: 3 failures before, 7 tests pass after.
Details: `astra-w1/red.txt`, `astra-w1/green.txt`.

## Baseline
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - facts/agent-prompt-delivery-no-duplicates.md: missing recommended frontmatter key(s): description
  - facts/author-never-self-merges.md: missing recommended frontmatter key(s): title, description
  - facts/codex-as-second-harness.md: missing recommended frontmatter key(s): description
  - facts/director-advisor-seats.md: missing recommended frontmatter key(s): title, description
  - facts/director-escalates-irreversible-and-outward.md: missing recommended frontmatter key(s): title, description
  - facts/director-holds-merge-authority.md: missing recommended frontmatter key(s): title, description
  - facts/estate-work-selection-rule.md: missing recommended frontmatter key(s): title, description
  - facts/expensive-models-delegate.md: missing recommended frontmatter key(s): title, description
  - facts/knowledge-architecture-operating-design.md: missing recommended frontmatter key(s): title, description
  - facts/never-submit-pane-text-you-did-not-type.md: missing recommended frontmatter key(s): description
  - facts/no-blockers-work-seams.md: missing recommended frontmatter key(s): description
  - facts/progressive-disclosure-in-code-and-docs.md: missing recommended frontmatter key(s): title, description
  - facts/topics-not-to-raise.md: missing recommended frontmatter key(s): description
  - facts/two-tmux-sessions-must-converge.md: missing recommended frontmatter key(s): title, description

Contract holds: no hard violations.
```

Measured 119 facts.

| Old slug | New ID |
|---|---|
| memory-conventions | 202607120001 |
| python-package-manager-uv | 202607120002 |
| git-commit-style | 202607120003 |
| no-flattery-in-analysis | 202607120004 |
| model-subscriptions | 202607130001 |
| audit-inherited-repo-patterns | 202607180001 |
| verify-in-browser-before-claiming-fixed | 202607190001 |
| topics-not-to-raise | 202607270001 |
| hill90-one-keycloak | 202607290001 |
| token-budget-discipline | 202607290002 |
| hill90-jon-keycloak-password | 202607300001 |
| hill90-app-public-cleanup-precondition | 202607310001 |
| green-ci-is-not-evidence | 202608040001 |
| verify-against-parent-not-just-green | 202608040002 |
| hill90-cross-project-attribution-boundary | 202608080001 |
| hill90-documentation-cadence | 202608080002 |
| hill90-orchestration-contract | 202608090001 |
| agent-repository-split | 202608090002 |
| agent-supervisor-autonomy-grant | 202608100001 |
| agent-supervisor-lane-architecture | 202608100002 |
| acp-transport-adapter-framing | 202608100003 |
| herdr-build-vs-adopt-open | 202608100004 |
| loop-mechanism-layered-design | 202608100005 |
| loop-throughput-comes-from-lanes-not-cadence | 202608100006 |
| supervisor-loop-never-stops | 202608100007 |
| harness-estate-is-not-hill90 | 202608110001 |
| independent-reviewers-find-disjoint-defects | 202608110002 |
| docs-that-assert-what-code-lacks | 202608110003 |
| github-closing-keywords-ignore-negation | 202608110004 |
| two-dot-diff-is-not-what-a-merge-does | 202608110005 |
| a-documented-command-is-code | 202608110006 |
| correct-the-label-not-just-the-body | 202608110007 |
| a-tool-nothing-calls-is-not-a-guard | 202608110008 |
| model-cost-preference | 202608110009 |
| a-conflicted-pr-branch-runs-no-ci-at-all | 202608110010 |
| delivered-is-a-claim-about-someone-elses-state | 202608120001 |
| tmux-is-not-a-database | 202608120002 |
| remembered-word-banana | 202608130001 |
| jon-qas-look-and-feel-agents-qa-function | 202608150001 |
| aesthetics-must-be-cheap-to-change | 202608150002 |
| usage-credits-are-a-ups-not-a-fuel-tank | 202608150003 |
| memory-needs-its-own-loop | 202608150004 |
| the-system-builds-the-system | 202608150005 |
| transcripts-are-a-lossy-record-of-intent | 202608160001 |
| five-engineering-disciplines-at-once | 202608160002 |
| ai-only-for-reasoning | 202608160003 |
| future-survives-web-frontend | 202608160004 |
| layout-navbar-outside-tmux | 202608160005 |
| multiplexer-keep-tmux | 202608160006 |
| prompt-corpus | 202608160007 |
| quality-polish | 202608160008 |
| render-live | 202608160009 |
| intent-as-a-constraint-solve | 202608160010 |
| premature-lock-on-and-signal-gated-loops | 202608160011 |
| cheap-probe-before-heavy-process | 202608200001 |
| codex-as-second-harness | 202608200002 |
| framework-build-dont-adopt | 202608200003 |
| prp-keep-three-parts-drop-the-pipeline | 202608200004 |
| do-not-quote-jon-verbatim | 202608220001 |
| estate-is-the-product | 202608220002 |
| exhaust-the-record-before-asking-jon | 202608220003 |
| go-not-shell | 202608220004 |
| skills-are-built-then-evaluated | 202608220005 |
| tui-one-to-one-with-hill90 | 202608220006 |
| agent-tui-and-supervisor-become-agent-estate | 202608230001 |
| agent-tui-goes-public-with-screenshots | 202608230002 |
| chat-is-a-room-with-mentions | 202608230003 |
| code-mode-for-mcp-is-backlogged | 202608230004 |
| harness-memory-override-is-later | 202608230005 |
| identity-guards-trade-false-positives-for-false-negatives | 202608230006 |
| integration-was-the-bottleneck-not-production | 202608230007 |
| lanes-and-chat-should-merge | 202608230008 |
| parked-decisions-artifact-2026-08-23 | 202608230009 |
| per-agent-memory-and-knowledge-graph | 202608230010 |
| product-is-called-the-estate | 202608230011 |
| rag-is-coming-design-for-it-now | 202608230012 |
| self-asserted-identity-that-becomes-evidence-launders-it | 202608230013 |
| silent-success-is-the-estate-failure-mode | 202608230014 |
| which-thinking-skill-to-reach-for | 202608230015 |
| a-locked-keychain-looks-like-a-tmux-session-problem | 202608240001 |
| a-proposed-fix-is-a-hypothesis-until-a-scratch-worktree-runs-it | 202608240002 |
| dispatch-through-the-tool-not-into-a-pane | 202608240003 |
| estate-burn-rate-measured-august-2026 | 202608240004 |
| find-on-this-mac-is-bfs-and-rejects-newermt | 202608240005 |
| guards-catch-absence-not-overreach | 202608240006 |
| installed-skills-are-frozen-copies-not-the-repo | 202608240007 |
| measure-the-pr-head-not-the-local-tree | 202608240008 |
| never-write-to-a-credential-store | 202608240009 |
| page-jon-when-the-estate-is-human-blocked | 202608240010 |
| scrollback-is-history-not-state | 202608240011 |
| second-lens-before-publishing-a-number | 202608240012 |
| the-corpus-is-incomplete-in-the-dangerous-direction | 202608240013 |
| the-shell-suite-writes-into-live-supervisor-state | 202608240014 |
| a-ledger-status-says-nothing-about-whether-a-lane-is-alive | 202608250001 |
| a-review-lane-stamp-is-a-tmux-lane-id-not-a-branch-name | 202608250002 |
| a-shell-loop-can-print-passes-from-commands-that-never-ran | 202608250003 |
| counting-claude-processes-counts-plumbing-not-agents | 202608250004 |
| git-cherry-lies-across-a-squash-merge | 202608250005 |
| host-load-can-be-orphaned-completion-helpers | 202608250006 |
| estate-work-selection-rule | 202608270001 |
| progressive-disclosure-in-code-and-docs | 202608270002 |
| director-advisor-seats | 202608290001 |
| expensive-models-delegate | 202608290002 |
| two-tmux-sessions-must-converge | 202608290003 |
| check-the-directive-before-the-work | 202608300001 |
| author-never-self-merges | 202609020001 |
| director-escalates-irreversible-and-outward | 202609020002 |
| director-holds-merge-authority | 202609020003 |
| knowledge-compounding-pipeline | 202609040001 |
| second-brain-basic-memory-edit-path | 202609040002 |
| knowledge-architecture-operating-design | 202609050001 |
| seed-knowledge-source-artifacts | 202609050002 |
| tail-only-status-checks | 202609050003 |
| no-blockers-work-seams | 202609050004 |
| knowledge-system-spans-repos | 202609060001 |
| never-submit-pane-text-you-did-not-type | 202609060002 |
| agent-prompt-delivery-no-duplicates | 202609060003 |
| game-speed-with-backups | 202609060004 |
| estate-is-skills-practice-not-a-product-to-sell | 202609060005 |

## Batch 1: 15 moved; exit 0
Manifests: `astra-w1/batch-01/checksums.json`, `after-checksums.json`.
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - ../01 - Notes/202607270001.md: missing recommended frontmatter key(s): description
  - facts/agent-prompt-delivery-no-duplicates.md: missing recommended frontmatter key(s): description
  - facts/author-never-self-merges.md: missing recommended frontmatter key(s): title, description
  - facts/codex-as-second-harness.md: missing recommended frontmatter key(s): description
  - facts/director-advisor-seats.md: missing recommended frontmatter key(s): title, description
  - facts/director-escalates-irreversible-and-outward.md: missing recommended frontmatter key(s): title, description
  - facts/director-holds-merge-authority.md: missing recommended frontmatter key(s): title, description
  - facts/estate-work-selection-rule.md: missing recommended frontmatter key(s): title, description
  - facts/expensive-models-delegate.md: missing recommended frontmatter key(s): title, description
  - facts/knowledge-architecture-operating-design.md: missing recommended frontmatter key(s): title, description
  - facts/never-submit-pane-text-you-did-not-type.md: missing recommended frontmatter key(s): description
  - facts/no-blockers-work-seams.md: missing recommended frontmatter key(s): description
  - facts/progressive-disclosure-in-code-and-docs.md: missing recommended frontmatter key(s): title, description
  - facts/two-tmux-sessions-must-converge.md: missing recommended frontmatter key(s): title, description

Contract holds: no hard violations.
```

## Batch 2: 15 moved; exit 0
Manifests: `astra-w1/batch-02/checksums.json`, `after-checksums.json`.
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - ../01 - Notes/202607270001.md: missing recommended frontmatter key(s): description
  - facts/agent-prompt-delivery-no-duplicates.md: missing recommended frontmatter key(s): description
  - facts/author-never-self-merges.md: missing recommended frontmatter key(s): title, description
  - facts/codex-as-second-harness.md: missing recommended frontmatter key(s): description
  - facts/director-advisor-seats.md: missing recommended frontmatter key(s): title, description
  - facts/director-escalates-irreversible-and-outward.md: missing recommended frontmatter key(s): title, description
  - facts/director-holds-merge-authority.md: missing recommended frontmatter key(s): title, description
  - facts/estate-work-selection-rule.md: missing recommended frontmatter key(s): title, description
  - facts/expensive-models-delegate.md: missing recommended frontmatter key(s): title, description
  - facts/knowledge-architecture-operating-design.md: missing recommended frontmatter key(s): title, description
  - facts/never-submit-pane-text-you-did-not-type.md: missing recommended frontmatter key(s): description
  - facts/no-blockers-work-seams.md: missing recommended frontmatter key(s): description
  - facts/progressive-disclosure-in-code-and-docs.md: missing recommended frontmatter key(s): title, description
  - facts/two-tmux-sessions-must-converge.md: missing recommended frontmatter key(s): title, description

Contract holds: no hard violations.
```

## Batch 3: 15 moved; exit 0
Manifests: `astra-w1/batch-03/checksums.json`, `after-checksums.json`.
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - ../01 - Notes/202607270001.md: missing recommended frontmatter key(s): description
  - facts/agent-prompt-delivery-no-duplicates.md: missing recommended frontmatter key(s): description
  - facts/author-never-self-merges.md: missing recommended frontmatter key(s): title, description
  - facts/codex-as-second-harness.md: missing recommended frontmatter key(s): description
  - facts/director-advisor-seats.md: missing recommended frontmatter key(s): title, description
  - facts/director-escalates-irreversible-and-outward.md: missing recommended frontmatter key(s): title, description
  - facts/director-holds-merge-authority.md: missing recommended frontmatter key(s): title, description
  - facts/estate-work-selection-rule.md: missing recommended frontmatter key(s): title, description
  - facts/expensive-models-delegate.md: missing recommended frontmatter key(s): title, description
  - facts/knowledge-architecture-operating-design.md: missing recommended frontmatter key(s): title, description
  - facts/never-submit-pane-text-you-did-not-type.md: missing recommended frontmatter key(s): description
  - facts/no-blockers-work-seams.md: missing recommended frontmatter key(s): description
  - facts/progressive-disclosure-in-code-and-docs.md: missing recommended frontmatter key(s): title, description
  - facts/two-tmux-sessions-must-converge.md: missing recommended frontmatter key(s): title, description

Contract holds: no hard violations.
```

## Batch 4: 15 moved; exit 0
Manifests: `astra-w1/batch-04/checksums.json`, `after-checksums.json`.
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - ../01 - Notes/202607270001.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202608200002.md: missing recommended frontmatter key(s): description
  - facts/agent-prompt-delivery-no-duplicates.md: missing recommended frontmatter key(s): description
  - facts/author-never-self-merges.md: missing recommended frontmatter key(s): title, description
  - facts/director-advisor-seats.md: missing recommended frontmatter key(s): title, description
  - facts/director-escalates-irreversible-and-outward.md: missing recommended frontmatter key(s): title, description
  - facts/director-holds-merge-authority.md: missing recommended frontmatter key(s): title, description
  - facts/estate-work-selection-rule.md: missing recommended frontmatter key(s): title, description
  - facts/expensive-models-delegate.md: missing recommended frontmatter key(s): title, description
  - facts/knowledge-architecture-operating-design.md: missing recommended frontmatter key(s): title, description
  - facts/never-submit-pane-text-you-did-not-type.md: missing recommended frontmatter key(s): description
  - facts/no-blockers-work-seams.md: missing recommended frontmatter key(s): description
  - facts/progressive-disclosure-in-code-and-docs.md: missing recommended frontmatter key(s): title, description
  - facts/two-tmux-sessions-must-converge.md: missing recommended frontmatter key(s): title, description

Contract holds: no hard violations.
```

## Batch 5: 15 moved; exit 0
Manifests: `astra-w1/batch-05/checksums.json`, `after-checksums.json`.
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - ../01 - Notes/202607270001.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202608200002.md: missing recommended frontmatter key(s): description
  - facts/agent-prompt-delivery-no-duplicates.md: missing recommended frontmatter key(s): description
  - facts/author-never-self-merges.md: missing recommended frontmatter key(s): title, description
  - facts/director-advisor-seats.md: missing recommended frontmatter key(s): title, description
  - facts/director-escalates-irreversible-and-outward.md: missing recommended frontmatter key(s): title, description
  - facts/director-holds-merge-authority.md: missing recommended frontmatter key(s): title, description
  - facts/estate-work-selection-rule.md: missing recommended frontmatter key(s): title, description
  - facts/expensive-models-delegate.md: missing recommended frontmatter key(s): title, description
  - facts/knowledge-architecture-operating-design.md: missing recommended frontmatter key(s): title, description
  - facts/never-submit-pane-text-you-did-not-type.md: missing recommended frontmatter key(s): description
  - facts/no-blockers-work-seams.md: missing recommended frontmatter key(s): description
  - facts/progressive-disclosure-in-code-and-docs.md: missing recommended frontmatter key(s): title, description
  - facts/two-tmux-sessions-must-converge.md: missing recommended frontmatter key(s): title, description

Contract holds: no hard violations.
```

## Batch 6: 15 moved; exit 0
Manifests: `astra-w1/batch-06/checksums.json`, `after-checksums.json`.
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - ../01 - Notes/202607270001.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202608200002.md: missing recommended frontmatter key(s): description
  - facts/agent-prompt-delivery-no-duplicates.md: missing recommended frontmatter key(s): description
  - facts/author-never-self-merges.md: missing recommended frontmatter key(s): title, description
  - facts/director-advisor-seats.md: missing recommended frontmatter key(s): title, description
  - facts/director-escalates-irreversible-and-outward.md: missing recommended frontmatter key(s): title, description
  - facts/director-holds-merge-authority.md: missing recommended frontmatter key(s): title, description
  - facts/estate-work-selection-rule.md: missing recommended frontmatter key(s): title, description
  - facts/expensive-models-delegate.md: missing recommended frontmatter key(s): title, description
  - facts/knowledge-architecture-operating-design.md: missing recommended frontmatter key(s): title, description
  - facts/never-submit-pane-text-you-did-not-type.md: missing recommended frontmatter key(s): description
  - facts/no-blockers-work-seams.md: missing recommended frontmatter key(s): description
  - facts/progressive-disclosure-in-code-and-docs.md: missing recommended frontmatter key(s): title, description
  - facts/two-tmux-sessions-must-converge.md: missing recommended frontmatter key(s): title, description

Contract holds: no hard violations.
```

## Batch 7: 15 moved; exit 0
Manifests: `astra-w1/batch-07/checksums.json`, `after-checksums.json`.
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - ../01 - Notes/202607270001.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202608200002.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202608270001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608270002.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608290001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608290002.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608290003.md: missing recommended frontmatter key(s): title, description
  - facts/agent-prompt-delivery-no-duplicates.md: missing recommended frontmatter key(s): description
  - facts/author-never-self-merges.md: missing recommended frontmatter key(s): title, description
  - facts/director-escalates-irreversible-and-outward.md: missing recommended frontmatter key(s): title, description
  - facts/director-holds-merge-authority.md: missing recommended frontmatter key(s): title, description
  - facts/knowledge-architecture-operating-design.md: missing recommended frontmatter key(s): title, description
  - facts/never-submit-pane-text-you-did-not-type.md: missing recommended frontmatter key(s): description
  - facts/no-blockers-work-seams.md: missing recommended frontmatter key(s): description

Contract holds: no hard violations.
```

## Batch 8: 14 moved; exit 0
Manifests: `astra-w1/batch-08/checksums.json`, `after-checksums.json`.
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - ../01 - Notes/202607270001.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202608200002.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202608270001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608270002.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608290001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608290002.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608290003.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609020001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609020002.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609020003.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609050001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609050004.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202609060002.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202609060003.md: missing recommended frontmatter key(s): description

Contract holds: no hard violations.
```

Migration complete: 119 moved; remaining facts files: 0.
Obsidian alias spot checks and instruction updates pending.

## Instruction surfaces

Backed up and updated (checksums: `astra-w1/instruction-backup/checksums.json`):
- /Users/jon/.agents/skills/memory-conventions/SKILL.md
- /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/ROUTING.md
- /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/LIFECYCLE.md
- /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/INDEX-CONTRACT.md
- /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/01 - Notes/202607120001.md
- /Users/jon/.claude/CLAUDE.md
- /Users/jon/.claude/rules/global.md

W4 owns the canonical dotfiles global instruction change. The installed memory skill was updated under W1; the Skills repository remains index-only as instructed. A later skill release must retain this convention.

## Obsidian alias check — FAIL

`obsidian vault="Agent Memory" eval` using `app.metadataCache.getFirstLinkpathDest(alias,"agent/index.md")` returned no destination for memory-conventions, python-package-manager-uv and git-commit-style. Notes are readable by their ID paths; alias metadata exists, but pre-existing bare wikilinks are not proven compatible. No wikilinks were bulk rewritten.

Preflight encountered one pre-existing malformed YAML title; migration reads only required ID fields and preserves original frontmatter/body bytes apart from added IDs/aliases. No migration batch failed or required rollback.

## Navigation path sweep

All known root/area indexes and agent-level docs scanned. Found/changed surfaces and backups: `astra-w1/navigation-backup/manifest.json`. 4 files had concrete Markdown paths corrected. Existing bare wikilinks remain untouched.
```
Checked 119 fact file(s) against /Users/jon/Library/Mobile Documents/iCloud~md~obsidian/Documents/Agent Memory/agent/index.md

Warnings (14, non-blocking):
  - ../01 - Notes/202607270001.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202608200002.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202608270001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608270002.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608290001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608290002.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202608290003.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609020001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609020002.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609020003.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609050001.md: missing recommended frontmatter key(s): title, description
  - ../01 - Notes/202609050004.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202609060002.md: missing recommended frontmatter key(s): description
  - ../01 - Notes/202609060003.md: missing recommended frontmatter key(s): description

Contract holds: no hard violations.
```

## Consumer compatibility limitation

The existing main-branch standing-law loader still reads a declared slug under
`agent/facts/` and pins that file's original bytes. The requested migration removes
that path and adds frontmatter, so main's standing-law lookup is not compatible
with this new layout. This push does not change standing-law membership, hashes,
caps or loading code, per the execution brief. Dispatches against the migrated
live vault require a separately reviewed compatibility fix. W3's gate used an
isolated copy of the original bytes, not a live-vault workaround.
