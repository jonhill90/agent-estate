package board

import "testing"

func TestParseRepositoriesMatchesCliShape(t *testing.T) {
	env := "dots=/home/x/agent-dotfiles=jonhill90/agent-dotfiles:sup=/home/x/agent-supervisor=jonhill90/agent-supervisor"
	got := ParseRepositories(env)
	if len(got) != 2 {
		t.Fatalf("got %d repos, want 2: %+v", len(got), got)
	}
	if got[0] != (Repo{Label: "dots", Owner: "jonhill90", Name: "agent-dotfiles"}) {
		t.Errorf("first repo = %+v", got[0])
	}
	if got[1] != (Repo{Label: "sup", Owner: "jonhill90", Name: "agent-supervisor"}) {
		t.Errorf("second repo = %+v", got[1])
	}
}

func TestParseRepositoriesEmptyIsNil(t *testing.T) {
	if got := ParseRepositories(""); got != nil {
		t.Errorf("empty env = %+v, want nil", got)
	}
	if got := ParseRepositories("   "); got != nil {
		t.Errorf("blank env = %+v, want nil", got)
	}
}

func TestParseRepositoriesSkipsMalformedEntries(t *testing.T) {
	// Mirrors cli.py's own behaviour: an entry without exactly
	// name=path=owner/repo is ignored, not fatal to the rest of the list.
	env := "bad-entry:dots=/x=jonhill90/agent-dotfiles:also-bad=missing-slash"
	got := ParseRepositories(env)
	if len(got) != 1 {
		t.Fatalf("got %d repos, want 1: %+v", len(got), got)
	}
	if got[0].Name != "agent-dotfiles" {
		t.Errorf("got %+v", got[0])
	}
}

func TestReposForFallsBackToDefault(t *testing.T) {
	got := ReposFor("", nil)
	if len(got) != len(DefaultRepos) {
		t.Fatalf("got %d repos, want %d (DefaultRepos)", len(got), len(DefaultRepos))
	}
}

func TestReposForUnionsDiscoveredRepos(t *testing.T) {
	// agent-tui is absent from DefaultRepos (repo.go's own doc comment) --
	// this is the exact gap ReposFor's discovered-repo union exists to
	// close using real ledger data, never a hardcoded addition.
	discovered := []Repo{{Label: "agent-tui", Owner: "jonhill90", Name: "agent-tui"}}
	got := ReposFor("", discovered)
	if len(got) != len(DefaultRepos)+1 {
		t.Fatalf("got %d repos, want %d", len(got), len(DefaultRepos)+1)
	}
	found := false
	for _, r := range got {
		if r.GitHubID() == "jonhill90/agent-tui" {
			found = true
		}
	}
	if !found {
		t.Errorf("agent-tui missing from %+v", got)
	}
}

func TestReposForDiscoveredDedupesAgainstBase(t *testing.T) {
	discovered := []Repo{{Label: "x", Owner: "jonhill90", Name: "agent-supervisor"}} // already in DefaultRepos
	got := ReposFor("", discovered)
	if len(got) != len(DefaultRepos) {
		t.Fatalf("got %d repos, want %d (no duplicate)", len(got), len(DefaultRepos))
	}
}

func TestReposForEnvOverrideAlsoUnionsDiscovered(t *testing.T) {
	env := "sup=/x=jonhill90/agent-supervisor"
	discovered := []Repo{{Owner: "jonhill90", Name: "agent-tui"}}
	got := ReposFor(env, discovered)
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 repos", got)
	}
}
