# What a turn actually costs — what can be observed, harness by harness

Phase 4, #975. This is a finding, not a design: what each harness genuinely
reports about one invocation, with the command and its real output pasted for
every claim. Measured 2026-09-03 against `claude` (Claude Code CLI, model
`claude-sonnet-5`), `codex` (`codex` CLI v0.149.0, model `gpt-5.6-terra`), and
`ccusage` (via `npx ccusage`, no local install on this machine).

## claude: reports its own dollar cost, per turn, on stdout

```
$ echo "reply with the single word pong, nothing else" | claude -p --output-format json --dangerously-skip-permissions
{"is_error":false,"duration_api_ms":2220,"num_turns":1,"stop_reason":"end_turn",
 "session_id":"d01a8703-42b9-4006-a3d0-83062d7d4339","total_cost_usd":0.1883826,
 "usage":{"input_tokens":2,"cache_creation_input_tokens":30393,
          "cache_read_input_tokens":17892,"output_tokens":4, ...},
 "modelUsage":{"claude-haiku-4-5-20251001":{...,"costUSD":0.000591},
               "claude-sonnet-5":{...,"costUSD":0.1877916}},
 "result":"pong", ...}
```

This is the exact JSON envelope `internal/harness.claudeResult` already
parses for the `result` field (`src/estate/internal/harness/harness.go`) —
`total_cost_usd` and `usage` sit right next to it in the same payload,
already on the estate's own stdout capture, for the exact process the estate
runs. **This is Anthropic's own billed figure**, not a token count multiplied
by a price this codebase would have to keep in sync. Nothing new to shell out
to; nothing to poll after the fact.

## codex: no dollar figure, but real per-turn token counts if `--json` is passed

Without `--json` (today's `internal/harness.codex.Start` invocation), codex
prints a human-readable transcript ending in a `tokens used` line and nothing
machine-parseable:

```
$ codex exec --skip-git-repo-check --sandbox workspace-write --output-last-message /tmp/x - <<< "reply pong"
...
codex
pong
tokens used
20,417
```

With `--json` added (JSONL events on stdout, `--output-last-message` still
honored simultaneously — verified together in one run):

```
$ codex exec --json --output-last-message /tmp/x --skip-git-repo-check --sandbox workspace-write - <<< "reply pong"
{"type":"thread.started","thread_id":"01a06826-ac92-7b62-8418-247dee57b779"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"pong"}}
{"type":"turn.completed","usage":{"input_tokens":27131,"cached_input_tokens":11008,
                                   "cache_write_input_tokens":0,"output_tokens":5,
                                   "reasoning_output_tokens":0}}
```

`turn.completed.usage` is real, per-invocation, and reported by the harness
itself — but it is tokens, never a dollar figure. **Codex reports no cost in
USD anywhere in its own output.** Any dollar number for a codex turn would
have to come from multiplying these tokens by a price this codebase assumes
— exactly the estimating the issue says not to do. So a codex turn's `Spend`
can honestly carry token counts and nothing under `CostUSD`.

## codexbar: one provider, one instant, not per-turn

```
$ codexbar usage --provider claude --json
[{"provider":"claude","usage":{"primary":{"windowMinutes":300,"usedPercent":50,...},
                                "secondary":{"windowMinutes":10080,"usedPercent":4,...}}}]
```

Confirms the issue's own claim: two percentages for the `claude` provider,
now, with no way to attribute the delta to one specific turn. This is what
`internal/quota.Read` already reads. It has no codex equivalent
(`codexbar usage --provider claude` names the OAuth-integration name
`claude`, not a generic "current harness" concept), and nothing here changes
that seam.

## ccusage: a full-history log-scraper, with its own separate cost math

`ccusage session --json --id <session-id>` filters Claude Code's own local
transcript log by session id and gives a per-session total:

```
$ npx ccusage session --json --id d01a8703-42b9-4006-a3d0-83062d7d4339
{"entries":[{"cacheCreationTokens":30393,"cacheReadTokens":17892,"costUSD":0.0,
             "inputTokens":2,"model":"claude-sonnet-5","outputTokens":4, ...}],
 "sessionId":"d01a8703-...","totalCost":0.1251944,"totalTokens":48291}
```

**`totalCost` (0.1251944) does not match `total_cost_usd` from the same
turn's own `claude -p` output (0.1883826)** — ccusage prices tokens against
its own bundled rate table and does not fold in the haiku sub-agent call
Claude Code's own accounting counted (`modelUsage.claude-haiku-4-5-20251001`
above); the two are computed by different code with different inputs. This
is exactly the kind of number that looks authoritative and is not
reconcilable without knowing which one you're reading. `ccusage codex
session --json` exists too and also computes `costUSD` from tokens against a
bundled price table — for codex this actually is an estimate, since codex
itself never reports one; nothing here should call ccusage's codex cost
"real" the way `claude -p`'s `total_cost_usd` is real. `ccusage session`'s
`--id` flag has no codex equivalent — codex sessions are found by matching
`sessionFile`/`sessionId` against the harness's own emitted thread id, a
correlation step, not a filter.

`ccusage` reads the CLI's on-disk transcript log after the fact; it is not
faster or more authoritative than reading `claude -p`'s own stdout in the
same process the estate already runs, and it requires either a local
install or `npx` (network on first use, and the version pin only whatever
`npx` resolves). It is the right tool for a retrospective sweep over
history the estate did not itself dispatch (which is what
`src/tui/internal/cost` uses it for); it is the wrong tool for attributing
one dispatched turn's cost, since `claude -p` already says its own number
directly.

## What this means for a spend ledger

| harness | dollar cost, per turn | token counts, per turn | source |
|---|---|---|---|
| claude | **yes** — `total_cost_usd`, Anthropic's own billed figure | yes — `usage.*` | same stdout envelope the estate already reads for `result` |
| codex | **no** — codex reports no dollar figure anywhere | yes — `turn.completed.usage` (`--json` must be added to the invocation) | stdout, once `--json` is added |

So a per-turn spend ledger can *honestly* carry:
- `claude`: a real, harness-reported `CostUSD`, plus token counts.
- `codex`: token counts only, `CostUSD` absent (typed absence, not a zero
  or an estimate).

This is implemented in `internal/harness.Turn.Spend` (a new field alongside
the existing `Result`, same "read what the harness itself produced" shape)
and recorded on `ledger.Record` as `spend_cost_usd` /
`spend_input_tokens` / `spend_output_tokens` / `spend_cache_read_tokens` /
`spend_cache_creation_tokens`, all `omitempty` pointers so "not reported" is
distinguishable from "reported as zero" — the same discipline
`src/tui/internal/cost.Figure.Known` already uses for this exact problem.
`internal/quota`'s provider-percentage reading is untouched; this is a
different, narrower kind of number (per-turn, not per-provider-window) and
does not replace it.
