---
okf_version: "0.2"
---

# Measurement

* [OKF bundle pilot](okf-bundle-pilot-2026-08-23.md) - Measures whether an OKF-style docs/ bundle is worth doing across four repos -- a pilot on agent-tui, not an adoption.

# Design

* [PRD](PRD.md) - What agent-tui is for, in Jon's own framing -- not the technical design.
* [Spec — technical design](SPEC.md) - The technical design of what is actually built in agent-tui today.
* [Spec — shell build order](SPEC-shell.md) - The TUI shell build order, 1:1 with the hill90 web nav -- one independently shippable item per PR.
* [Spec — AgentBox execution mode](SPEC-agentbox-execution-mode.md) - Design brief for the AgentBox container execution driver, verified against the sibling AgentBox repo.

# Research

* [Per-agent memory storage evidence](RESEARCH-per-agent-memory-storage.md) - Evidence for the per-agent memory storage-format decision (agent-tui#116) -- does not choose a format; that stays reserved to Jon.
* [Agent memory / knowledge graph / OKF+RAG survey](research/agent-memory-knowledge-graph-survey.md) - Prior-art survey on agent memory, knowledge graphs, and the OKF+RAG router pattern (agent-tui#116) -- a survey, not an adoption recommendation.

# UI variants and spikes

* [Memory variants (knowledge graph view)](memoryvariants/README.md) - Knowledge-graph view of agent memory -- variants and a spike, gated BACKLOG behind agent-tui#61's own do-not-start condition.
* [UI variants (rail + board)](uivariants/README.md) - Six real rendered rail+board UI variants over hardcoded fake state, for Jon to pick from (agent-tui#62).
* [Lanechat variant comparison](lanechat-variant-comparison.md) - Three real, working screens for the combined Lanes+Chat surface, side by side for Jon to pick from (agent-tui#122).

# Test evidence

* [Nav-walk docs captures](navwalk-docs/README.md) - Nav-walk frame captures proving the Docs section's two destinations stopped being stubs (2026-08-22).

# CI / Guards

* [Tape-build guard](vhscheck-guard.md) - What `internal/vhscheck` checks, why it exists (agent-tui#132/agent-tui#133), how it tells a live `go build` from a comment mentioning a path, and how to run it.
