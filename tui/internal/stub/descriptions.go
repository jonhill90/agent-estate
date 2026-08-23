package stub

// Descriptions is one line per SPEC-shell.md destination that S4 does not
// wire to an existing screen -- Tasks (internal/board), Usage
// (internal/cost) and Lanes (internal/rail) are excluded here on purpose,
// since a real view already exists for those and a stub would hide it,
// the exact failure S5 exists to fix.
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

	// Connect. Storage/Discord/Secrets follow agent-b3.md's own template --
	// what it would show, what would have to exist, and that nothing of
	// the kind exists in this estate today -- rather than the generic,
	// presupposing text these three carried before ("Discord integration
	// configuration.", as though one existed to configure).
	"connections": "provider connections available to agents. (S10)",

	"storage": "would show object storage: buckets, sizes, and (a separate,\n" +
		"undecided question -- see agent-tui#101) object listings or\n" +
		"contents. For this to show anything, agent-tui would need a wired\n" +
		"adapter reading a real bucket API. No such adapter exists in this\n" +
		"codebase today -- verified: `git grep -i storage` turns up only\n" +
		"this nav entry, this stub, and their own tests, nothing that opens\n" +
		"a connection to anything. A real backend DOES exist elsewhere in\n" +
		"this estate (hill90-app's own MinIO/S3 deployment, its\n" +
		"services/api/src/routes/storage.ts), but whether and how much of\n" +
		"it this viewer may show is a decision about a credential surface,\n" +
		"not a stub-clearing change (agent-tui#101).",

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

	"secrets": "would show, at most, that a secret exists, its name/path,\n" +
		"its age and its last rotation -- never its value (see\n" +
		"agent-tui#101 for why). For this to show anything, agent-tui would\n" +
		"need a wired adapter reading a real secrets store. No such adapter\n" +
		"exists in this codebase today -- verified: `git grep -i secrets`\n" +
		"turns up only this nav entry, this stub, and their own tests. A\n" +
		"real backend DOES exist elsewhere in this estate (hill90-app's own\n" +
		"OpenBao/Vault-backed platform/vault/secrets-schema.yaml and its\n" +
		"services/api/src/routes/secrets.ts, which already draws the same\n" +
		"never-show-a-value line this stub recommends), but whether and how\n" +
		"much of it this viewer may show is a decision about a credential\n" +
		"surface, not a stub-clearing change (agent-tui#101).",

	// Admin
	"admin-services": "running services and their health. (S11)",
	"admin-profiles": "user and agent profiles. (S11)",
	"admin-users":    "accounts with access to the estate. (S11)",
	"dependencies":   "the package/service dependency graph. (S11)",
	"settings":       "estate-wide configuration. (S11)",
}
