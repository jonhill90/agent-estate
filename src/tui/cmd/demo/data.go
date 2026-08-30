package main

// Fake but plausible content for every nav destination.
//
// WHY THIS EXISTS: the shell currently renders `not built yet` for unwired
// routes. That is honest and it is useless to look at -- you cannot tell
// whether a layout is right from a stub. Every row here is invented, and the
// binary says so on screen, but the SHAPE is real: these are the columns and
// the density each view would actually carry, which is the thing worth
// reacting to before any of it is wired to live data.
type row []string

type view struct {
	title   string
	blurb   string
	headers row
	rows    []row
	notes   []string
}

var views = map[string]view{
	"home": {
		title: "Home",
		blurb: "Everything at a glance.",
		notes: []string{
			"4 agents running   ·   2 idle   ·   0 blocked",
			"12 open PRs across 4 repos   ·   6 merged today",
			"today  $134.46      week  7% of quota used",
			"",
			"↑/↓ move    Enter open    ←/→ collapse/expand    click anything",
		},
	},
	"dashboard": {
		title: "Dashboard", blurb: "Throughput and spend.",
		headers: row{"METRIC", "TODAY", "7-DAY", "TREND"},
		rows: []row{
			{"PRs merged", "6", "63", "▁▃▅▇▆▇█"},
			{"PRs opened", "8", "71", "▂▄▆█▅▆▇"},
			{"Agent hours", "9.2", "84.5", "▃▅▄▆▇▆█"},
			{"Spend", "$134.46", "$3,173", "▅▇▆█▇▆▃"},
			{"Quota used", "7%", "—", "▁▂▃▃▄▅▇"},
		},
	},
	"agents": {
		title: "Agents", blurb: "Every agent, what it holds, what it costs.",
		headers: row{"NAME", "MODEL", "STATE", "TASK", "MODE", "UP", "COST"},
		rows: []row{
			{"build-1", "sonnet", "● running", "S10 connectors view", "local", "1h04m", "$18.22"},
			{"build-2", "sonnet", "● running", "S11 admin section", "local", "1h04m", "$16.90"},
			{"build-3", "sonnet", "● running", "#74 shell routing", "local", "22m", "$7.41"},
			{"build-4", "sonnet", "● running", "#79 execution mode", "local", "22m", "$6.88"},
			{"reviewer-1", "opus", "○ idle", "—", "container", "3h11m", "$41.03"},
			{"knowledge", "sonnet", "○ idle", "—", "container", "3h11m", "$2.15"},
		},
	},
	"chat": {
		title: "Chat", blurb: "Live threads with agents.",
		headers: row{"THREAD", "WITH", "LAST", "MSGS"},
		rows: []row{
			{"nav collision postmortem", "build-3", "2m ago", "14"},
			{"why did the loop not merge", "supervisor", "51m ago", "9"},
			{"S12 execution modes", "build-4", "1h ago", "22"},
			{"vhs harness", "build-1", "6h ago", "7"},
		},
	},
	"tasks": {
		title: "Tasks", blurb: "The board.",
		headers: row{"", "ID", "TITLE", "REPO", "AGE"},
		rows: []row{
			{"▶", "#74", "S3 app shell + nav routing", "agent-tui", "9h"},
			{"▶", "#79", "S12 execution mode interface", "agent-tui", "8h"},
			{"◇", "#495", "lanes launch --strict-mcp-config", "agent-supervisor", "13h"},
			{"◇", "S10", "Connectors view", "agent-tui", "1h"},
			{"◇", "S11", "Admin section", "agent-tui", "1h"},
			{"✓", "#78", "S9 MCP servers view", "agent-tui", "merged"},
			{"✓", "#77", "S8 skills view", "agent-tui", "merged"},
			{"✓", "#73", "S1+S2 nav model + sidebar", "agent-tui", "merged"},
		},
	},
	"knowledge": {
		title: "Knowledge", blurb: "What the estate knows about you.",
		headers: row{"FACT", "TYPE", "UPDATED"},
		rows: []row{
			{"estate-is-the-product", "project", "today"},
			{"tui-one-to-one-with-hill90", "user", "today"},
			{"go-not-shell", "feedback", "today"},
			{"skills-are-built-then-evaluated", "user", "today"},
			{"do-not-quote-jon-verbatim", "feedback", "today"},
			{"tmux-is-not-a-database", "feedback", "Aug 19"},
			{"model-cost-preference", "user", "Aug 17"},
		},
		notes: []string{"63 facts   ·   lint clean   ·   0 orphans   ·   0 broken links"},
	},
	"library": {
		title: "Library", blurb: "Shared collections.",
		headers: row{"COLLECTION", "SOURCES", "CHUNKS", "OWNER"},
		rows: []row{
			{"prior art: agent harnesses", "4", "312", "jon"},
			{"hill90 platform runbooks", "11", "884", "jon"},
			{"loop engineering", "5", "196", "jon"},
		},
	},
	"skills": {
		title: "Skills", blurb: "Built dynamically, then evaluated. Unused means unevaluated.",
		headers: row{"SKILL", "INVOCATIONS", "LAST EVAL", "VERDICT"},
		rows: []row{
			{"ask-a-council", "2", "Aug 11", "keep"},
			{"verify-the-instrument", "22", "Aug 11", "keep"},
			{"sanity-check", "22", "Aug 12", "keep"},
			{"prompt-corpus", "3", "Aug 16", "keep"},
			{"safe-deletion", "22", "Aug 14", "keep"},
			{"failing-test-first", "7", "—", "unevaluated"},
			{"tdd", "0", "—", "unevaluated"},
			{"primer", "1", "—", "unevaluated"},
			{"close-the-loop", "0", "—", "unevaluated"},
		},
		notes: []string{"35 installed   ·   9 evaluated   ·   26 awaiting eval"},
	},
	"workflows": {
		title: "Workflows", blurb: "Multi-step agent pipelines.",
		headers: row{"WORKFLOW", "STEPS", "LAST RUN", "RESULT"},
		rows: []row{
			{"spec → build → review → merge", "4", "12m ago", "running"},
			{"mine prior art", "3", "9h ago", "ok"},
			{"corpus ingest + judge", "3", "yesterday", "ok"},
		},
	},
	"mcp-servers": {
		title: "MCP Servers", blurb: "What a lane MAY be given. Lanes launch --strict-mcp-config.",
		headers: row{"SERVER", "SCOPE", "REACHABLE"},
		rows: []row{
			{"context7", "global", "yes"},
			{"microsoft-learn", "global", "yes"},
			{"deepwiki", "global", "yes"},
			{"obsidian", "project", "yes"},
			{"github", "project", "yes"},
			{"playwright", "plugin", "yes"},
		},
	},
	"connections": {
		title: "Connections", blurb: "Provider credentials.",
		headers: row{"NAME", "PROVIDER", "SCOPE", "ADDED"},
		rows: []row{
			{"Platform OpenAI", "openai", "platform", "Aug 5"},
			{"Anthropic (sub)", "anthropic", "platform", "Jul 29"},
		},
	},
	"models": {
		title: "Models", blurb: "Routing and policy.",
		headers: row{"MODEL", "ROLE", "$/MTOK", "USED 7D"},
		rows: []row{
			{"claude-sonnet", "workers", "$3", "78%"},
			{"claude-opus", "review, council", "$15", "19%"},
			{"gpt-5.6-terra", "second opinion", "$5", "3%"},
		},
	},
	"storage":  {title: "Storage", blurb: "Buckets and volumes.", headers: row{"BUCKET", "OBJECTS", "SIZE"}, rows: []row{{"artifacts", "1,204", "3.1 GB"}, {"transcripts", "18,332", "812 MB"}}},
	"discord":  {title: "Discord", blurb: "Bot and channels.", headers: row{"CHANNEL", "PURPOSE", "STATUS"}, rows: []row{{"#estate", "notifications", "connected"}}},
	"secrets":  {title: "Secrets", blurb: "SOPS + OpenBao. Values never shown.", headers: row{"KEY", "STORE", "ROTATED"}, rows: []row{{"ANTHROPIC_API_KEY", "openbao", "Aug 1"}, {"HILL90_APP_DB_PASSWORD", "sops", "Jul 30"}}},
	"usage":    {title: "Usage", blurb: "Spend and quota.", headers: row{"WINDOW", "USED", "RESETS"}, rows: []row{{"5-hour", "6%", "2:30pm"}, {"weekly", "7%", "Aug 27 10am"}}, notes: []string{"today $134.46   ·   7-day $3,173", "cheapest day this week"}},
	"monitoring": {title: "Monitoring", blurb: "Health.", headers: row{"CHECK", "STATE", "LAST"}, rows: []row{{"build loop", "running", "2m"}, {"host pressure", "ok 0.76/core", "2m"}, {"quota gate", "ok 7%", "2m"}}},
	"api-docs":      {title: "API Docs", blurb: "Generated reference.", notes: []string{"not wired to a live source yet"}},
	"platform-docs": {title: "Platform Docs", blurb: "docs.hill90.com", notes: []string{"external link"}},
	"admin-services": {title: "Services", blurb: "Platform services.", headers: row{"SERVICE", "STATE", "UPTIME"}, rows: []row{{"traefik", "healthy", "13d"}, {"keycloak", "healthy", "13d"}, {"postgres", "healthy", "13d"}, {"openbao", "healthy", "13d"}, {"minio", "healthy", "13d"}}},
	"admin-profiles": {title: "Profiles", blurb: "Container profiles.", headers: row{"PROFILE", "CPUS", "MEM"}, rows: []row{{"standard", "2", "4g"}, {"heavy", "6", "12g"}}},
	"admin-users":    {title: "Users", blurb: "Realm platform.", headers: row{"USER", "ROLES"}, rows: []row{{"jon", "platform-admin, hill90-ui:admin"}, {"testuser01", "hill90-ui:user"}}},
	"dependencies":   {title: "Dependencies", blurb: "Tooling the agents rely on.", headers: row{"TOOL", "VERSION"}, rows: []row{{"go", "1.26.0"}, {"gh", "2.x"}, {"vhs", "0.11.0"}, {"claude", "2.1.220"}}},
	"settings":       {title: "Settings", blurb: "Preferences.", notes: []string{"theme, glyph set, grouping — all live in the rail today"}},
	"lanes":          {title: "Lanes", blurb: "tmux sessions. No web equivalent — this is what the web app cannot do.", headers: row{"SESSION", "WINDOW", "STATE"}, rows: []row{{"estate", "build-1", "● busy"}, {"estate", "build-2", "● busy"}, {"estate", "build-3", "● busy"}, {"estate", "build-4", "● busy"}, {"agent-supervisor", "supervisor", "○ idle"}}},
}
