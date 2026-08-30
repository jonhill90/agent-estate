// Package board projects the task board from three systems that already
// hold the facts -- GitHub issues/PRs (intent), the supervisor ledger's
// `tasks` table (execution), and `source_tasks` (the link between them) --
// plus the live "lanes" MCP payload agent-tui already fetches for the rail.
// Nothing here is a fourth store: every field on Card is recomputed from
// those sources on every fetch, never held across one and reused (see
// card.go's doc comment for the derivation rules, and agent-tui#6 for why
// that rule outranks every other decision in this package).
package board

import "strings"

// Repo names one GitHub repository this board watches, and where its
// GitHub identity is `Owner/Name`.
type Repo struct {
	Label string // short name, e.g. "agent-tui" -- cosmetic, used to group columns
	Owner string
	Name  string
}

// GitHubID returns "owner/repo", the shape `gh --repo` and source URLs use.
func (r Repo) GitHubID() string { return r.Owner + "/" + r.Name }

// DefaultRepos mirrors agent-supervisor's cli.py DEFAULT_REPOSITORIES
// literally (scripts/supervisor/cli.py) so an unset SUPERVISOR_REPOSITORIES
// behaves the same for this board as it does for the supervisor's own
// dispatch. It does NOT include agent-tui: measured against the live
// ledger, the supervisor's own built-in list has never included the repo
// this board ships from, even though source_tasks already holds five
// agent-tui rows (dispatched under a SUPERVISOR_REPOSITORIES override that
// was never persisted anywhere durable). ReposFor's discovered-repo merge
// below exists specifically so that gap can't silently drop cards for the
// repo the issue itself spans.
var DefaultRepos = []Repo{
	{Label: "agent-dotfiles", Owner: "jonhill90", Name: "agent-dotfiles"},
	{Label: "agent-supervisor", Owner: "jonhill90", Name: "agent-supervisor"},
	{Label: "skills", Owner: "jonhill90", Name: "skills"},
	{Label: "skills-private", Owner: "jonhill90", Name: "skills-private"},
	{Label: "agent-evals", Owner: "jonhill90", Name: "agent-evals"},
}

// ParseRepositories parses the SUPERVISOR_REPOSITORIES shape documented in
// agent-supervisor's .env.example and cli.py:_repositories_from_env --
// colon-separated `name=path=owner/repo` entries. path is accepted and
// ignored (this board never touches a checkout, only GitHub and the
// ledger); it is parsed so a value copied straight out of .env or the
// supervisor's own environment doesn't need editing to be reused here.
// An entry missing the owner/repo shape is skipped, not fatal -- matching
// cli.py's own "entry ignored" behaviour rather than refusing the whole
// list over one typo.
func ParseRepositories(env string) []Repo {
	env = strings.TrimSpace(env)
	if env == "" {
		return nil
	}
	var repos []Repo
	for _, entry := range strings.Split(env, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		fields := strings.Split(entry, "=")
		if len(fields) != 3 {
			continue
		}
		name, ownerRepo := fields[0], fields[2]
		owner, repoName, ok := strings.Cut(ownerRepo, "/")
		if !ok || owner == "" || repoName == "" {
			continue
		}
		repos = append(repos, Repo{Label: name, Owner: owner, Name: repoName})
	}
	return repos
}

// ReposFor returns the repo list to query: SUPERVISOR_REPOSITORIES's parse
// if it names any, else DefaultRepos -- exactly cli.py's own precedence --
// unioned with discovered, the distinct owner/repo pairs seen in
// source_tasks.source_url that the base list is missing. This is how the
// board reaches agent-tui today without hardcoding it: discovered is real
// data (ledger.go extracts it from every row's source_url), not a literal
// added to this file because Jon happened to name three repos in the issue.
func ReposFor(env string, discovered []Repo) []Repo {
	base := ParseRepositories(env)
	if base == nil {
		base = DefaultRepos
	}
	seen := make(map[string]bool, len(base))
	out := make([]Repo, len(base))
	copy(out, base)
	for _, r := range out {
		seen[strings.ToLower(r.GitHubID())] = true
	}
	for _, r := range discovered {
		key := strings.ToLower(r.GitHubID())
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}
