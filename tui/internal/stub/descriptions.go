package stub

// Descriptions is one line per SPEC-shell.md destination that S4 does not
// wire to an existing screen -- Tasks (internal/board), Usage
// (internal/cost) and Lanes (internal/rail) are excluded here on purpose,
// since a real view already exists for those and a stub would hide it,
// the exact failure S5 exists to fix. Every other destination in S1's tree
// is keyed here by its exact Label so the shell can look one up by nav
// item without this package needing to import internal/nav (S1, a
// separate item, not touched by this one).
//
// Each description says what the destination WILL show, sourced from its
// own numbered item below where one exists (S6-S11); destinations with no
// numbered item of their own (Home, Dashboard, Knowledge, Library, Storage,
// Discord, Secrets, API Docs, Platform Docs) get a description inferred
// from S1's grouping and the hill90 nav's own naming, since no later item
// specs them individually yet.
//
// Workflows and Monitoring are REMOVED from this map, not merely
// unlisted: w5f.md gave each a real pane (internal/workflows,
// internal/monitor), wired into internal/shell's routeToPane, so a stub
// here would now hide a real screen -- the exact failure this doc comment's
// own opening sentence says Tasks/Usage/Lanes are excluded to avoid.
// "Models" is gone entirely, from both this map and internal/nav's own
// tree (nav.Build's own doc comment on why) -- there is no destination left
// for it to describe.
var Descriptions = map[string]string{
	// Top level
	"Home":      "a landing overview with quick links across the estate.",
	"Dashboard": "at-a-glance metrics across agents, tasks and usage.",
	"Agents":    "agents from the supervisor daemon: id, model, state, current task, cost. (S6)",
	"Chat":      "a thread's list, transcript and composer against an agent. (S7)",
	"Knowledge": "knowledge base entries surfaced from the estate.",
	"Library":   "reusable prompts, templates and reference material.",

	// Build
	"Skills":      "skills from ~/.claude/skills and the skills repo: name, description, last eval result, invocation count. (S8)",
	"MCP Servers": "configured MCP servers, scope (global/project) and reachability. (S9)",

	// Connect
	"Connections": "provider connections available to agents. (S10)",
	"Storage":     "storage backends configured for the estate.",
	"Discord":     "Discord integration configuration.",
	"Secrets":     "secret values and the scopes that can read them.",

	// Docs
	"API Docs":      "generated API reference for the estate's services.",
	"Platform Docs": "platform documentation and guides.",

	// Admin
	"Services":     "running services and their health. (S11)",
	"Profiles":     "user and agent profiles. (S11)",
	"Users":        "accounts with access to the estate. (S11)",
	"Dependencies": "the package/service dependency graph. (S11)",
	"Settings":     "estate-wide configuration. (S11)",
}
