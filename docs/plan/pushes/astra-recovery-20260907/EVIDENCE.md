# Evidence and limits — Astra, 2026-09-07

Private planning record. During initial preparation, repo/vault/source state was read, not changed; no terminal input, implementation/repository tests or migrations occurred. Jon subsequently authorized director and Fable handoff messages; those were submitted to verified panes `%408` and `%407` after reading their current output. The separate handoff files record the updated scope. No worker-lane input or source/vault changes were made by Astra.

## Direct observations

`tmux list-windows -t director` resolved window 3 to `@293 fable`; `list-panes -t @293` resolved `%296 claude.exe`. `capture-pane -p -J -t @293 -S -360` showed Fable's recent conclusion that enforcement was the missing piece, followed by creation of `astra-review-brief.md`. The available capture did not show the earlier three alleged defenses. Their occurrence is reported by Fable's brief, not independently reconstructed here.

Read the entire review brief, actual projection producer, generated note examples, current knowledge docs, corpus schema/selected source-linked items, root vault index and relevant facts, P12 plan and recent iteration records. One bounded explorer traced code independently. Its claim that the live DB contains a candidate queue was corrected by direct schema inspection: support exists, but that table was not present in the inspected live DB.

```text
git status --short --branch
## main...origin/main [behind 3]
?? src/progress/progress

remote main: 8ba75ac4e626e6872c69b333355fb7f1c16f0465
open documentation PR: #1285, head 538c6e449fbaccfebd65b6b40018d1a126dbeceb

SELECT count(*),sum(weight!='retracted'),sum(weight='retracted') FROM items;
7311|3514|3797

SELECT weight,count(*) FROM items GROUP BY weight;
hard|2638
preference|876
retracted|3797

retracted AND needs_review: 135
of those, status_reason contains synthetic: 130
prompts: 9763
codex_provenance rows: 4360
newest recorded prompt UTC: 2026-09-06 02:57:18
```

`sqlite3 -readonly … '.schema items'` defines `weight` and `status`; **no `lifecycle` column**. The review brief's `WHERE lifecycle…` census queries cannot run against this database. Counts above use the actual schema. A `live_parameters` count is neither all items nor all hard items.

Read-only Python census over every `.md` in `01 - Notes/01p - Parameters`:

```text
files: 2638
generic descriptions: 2638, in five per-kind strings
directive=1341; parameter=958; correction=173; question=138; thought=28
repetitive 'Projection of corpus item' footer: 2638
type-plus-item-ID fallback titles: 1616
standing-rule tag: 2430
```

The title detector matches `Parameter|Directive|Correction|Question|Thought` followed by `it-<hex>` in the frontmatter title. An initial narrower detector for a bare item ID returned zero and was corrected after reading the actual title format. Fable's figure of 1,532 is not reproduced by this explicit criterion. Descriptions are five templates, not one byte-identical string. None of these counts alone determines semantic note quality.

Opened examples include `202606140001.md` (a one-time comparison directive with a type-plus-ID title and standing-rule tag), `202608050022.md` (a useful substantive parameter with generic wrapper), and `202608280006.md` (a specific historical correction with a type-plus-ID title). Their bodies demonstrate that wrapping defects do not make all underlying evidence worthless.

```text
go run ./src/estate knowledge query --private --json 'source:vault-fact knowledge'
exit: 0
state: matched
total_matched: 12
index_generated_at: 2026-09-05T09:49:19.929387Z
coverage.state: mixed
coverage reasons: vault stale; corpus stale; stars freshness unknown
returned vault permalinks: …/Agent Memory/agent/facts/…
```

The retired `agent/` directory was absent. The default shared index contained 112 vault facts and no catalogue source section; its file size was 3,821,547 bytes. This index was deliberately protected during previous pushes, so staleness is not evidence that its writer malfunctioned. It is evidence that this default read path does not reflect the migrated vault. No regeneration was attempted.

`main_candidates_register.go` still receives catalogue ID/locator/hash from caller flags; its comment describes replacing that bridge with the catalogue lookup. This file is unchanged between inspected local and remote main. The candidate acceptance trace warrants an integrated source-drift test; no live stale publication incident is claimed.

## Important limits

- Mechanically counted the full corpus and full projection directory. Did **not** semantically reread all 7,311 item bodies. Fable's all-items read/verification claim is not inherited as this review's work.
- Confirmed the 135 pending-review rows. Did not independently establish the brief's estimated 30–40 misclassified human statements, all emotional-signal claims, or all four historical cycles. Re-triage must inspect original context before changing status.
- Selected schema/query mistakes were corrected; there is no unqualified “all checks pass” claim. Repository tests were intentionally not run for this planning task.
- Current code, deployed files and the old shared index differ. The proposed SPEC labels those differences; a passing historical descriptive demo is not proof of today's operational behavior.
- Root `docs/product/PRD.md` and `SPEC.md` are dated August 30 and cover the supervisor. This packet supplies the updated knowledge slice for their owner to integrate, preserving unrelated product requirements.

## Packet verification

Follow-up at Jon's reminder: inspected director:1 and all three estate lanes by their discovered window IDs (`@1`, `@395`, `@396`, `@397`), with read-only captures only. Lane-c was visibly running dotfiles lint/test work. The execution plan now records the active ownership and makes assignment/integration by director:1 a prerequisite. GitHub read-back returned:

```text
estate #1285: OPEN, head 538c6e449fbaccfebd65b6b40018d1a126dbeceb
skills #305: MERGED, 80d9f8e8b85879d76a35b284502577ecc25cdb05
dotfiles #345: MERGED, 9648535c05d597bffc47aa1d1a12132655cfedce
```

No lane was interrupted, messaged or reassigned. This snapshot expires as the director continues; the plan requires a narrow state refresh before assigning overlapping work.

One existing explorer was reused for a bounded final plan check; no further agents were created. It found two sequencing defects: the final private index must follow publication, and expired directives must be tested through corpus as well as vault retrieval. Both were incorporated. This is a planning review, not runtime verification of the proposed system.

Actual document verification output:

```text
broken local links: 0
nonempty documents: 4
plan sections: 9 / 9
```
