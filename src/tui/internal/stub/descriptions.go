package stub

// Descriptions is one line per SPEC-shell.md destination that S4 does not
// wire to an existing screen -- Tasks (internal/board), Usage
// (internal/cost), Lanes (internal/rail) and, since agent-tui#101's
// decision landed, Secrets (internal/secrets) are excluded here on
// purpose, since a real view already exists for those and a stub would
// hide it, the exact failure S5 exists to fix.
//
// Keyed by internal/nav.Item.ID (e.g. "discord", "mcp-servers"), NOT by
// Label -- and this was a real, live bug until now, not a hypothetical
// one: shell.stubView looks up this map with `m.nav.Active()`, which
// returns the route id (internal/nav.Model.Active's own doc comment;
// confirmed by driving the Discord stub with testdata/vhs before this
// fix -- it rendered "not built yet -- no description recorded for this
// route", the exact fallback path, not the "Discord integration
// configuration." string that had been sitting in this map for anyone
// who read the source instead of pressing the key). Every entry below
// was re-keyed to the id nav.Build() actually emits for it.
var Descriptions = map[string]string{
	// Top level
	"home":      "a landing overview with quick links across the estate.",
	"dashboard": "at-a-glance metrics across agents, tasks and usage.",
	"agents":    "agents from the supervisor daemon: id, model, state, current task, cost. (S6)",
	"chat":      "a thread's list, transcript and composer against an agent. (S7)",
	"knowledge": "knowledge base entries surfaced from the estate.",
	"library":   "reusable prompts, templates and reference material.",

	// Build
	"skills":      "skills from ~/.claude/skills and the skills repo: name, description, last eval result, invocation count. (S8)",
	"mcp-servers": "configured MCP servers, scope (global/project) and reachability. (S9)",

	// Connect. Storage/Discord follow agent-b3.md's own template -- what
	// each would show, what would have to exist, and why nothing wires it
	// today -- rather than the generic, presupposing text they carried
	// before ("Discord integration configuration.", as though one existed
	// to configure). Secrets used to be a third entry here; agent-tui#101
	// decided it and internal/secrets is now a real pane (routeToPane's
	// own "secrets" entry), so it is gone from this map entirely, the same
	// way Workflows and Monitoring left it when w5f.md gave them real
	// panes.
	"connections": "provider connections available to agents. (S10)",

	"storage": "would show object storage: bucket names/sizes/counts, then\n" +
		"per-bucket object listings (names/sizes/timestamps, never\n" +
		"contents) -- agent-tui#101's decision approved exactly that much,\n" +
		"the same buckets-then-listings shape Secrets got for its own two\n" +
		"lowest levels. Still a stub, though, for a reason distinct from\n" +
		"the decision itself: unlike Secrets' schema.yaml (a plain,\n" +
		"credential-free file this estate already keeps in git), Storage's\n" +
		"real backend (hill90-app's own MinIO/S3 deployment,\n" +
		"services/api/src/routes/storage.ts) has no local, credential-free\n" +
		"equivalent -- every level, even bucket names, requires a live,\n" +
		"authenticated S3 client call this repo has no seam for (see\n" +
		"internal/connectors' own doc comment for why this repo declines\n" +
		"to invent one). Object contents (agent-tui#101's level 3) remain\n" +
		"deliberately out of scope regardless -- not decided here, not\n" +
		"waiting only on a seam.",

	"discord": "would show connected channels and their status, the same\n" +
		"shape cmd/demo's own fixture uses to illustrate it. For this to\n" +
		"show anything, a real Discord bot/webhook integration would have\n" +
		"to exist somewhere in this estate. None does -- verified:\n" +
		"`git grep -i discord` across every repo this box has (agent-tui,\n" +
		"agent-supervisor, agent-dotfiles) turns up only this nav entry,\n" +
		"this stub, one doc line in agent-supervisor's notify.sh listing\n" +
		"Discord as a DEFERRED future notification channel (never\n" +
		"implemented, no client, no config, no credential), and incidental\n" +
		"mentions of the word in chat transcripts. This destination exists\n" +
		"only because the web app's own nav-items.ts lists it and this tree\n" +
		"is required to mirror that 1:1 -- not because anything behind it\n" +
		"is real or planned.",

	// Admin
	"admin-services": "running services and their health. (S11)",
	"admin-profiles": "user and agent profiles. (S11)",
	"admin-users":    "accounts with access to the estate. (S11)",
	"dependencies":   "the package/service dependency graph. (S11)",
	"settings":       "estate-wide configuration. (S11)",
}
