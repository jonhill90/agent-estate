package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// WHY THIS FILE EXISTS (agent-estate#1009's review). GHLanded's own
// subprocess was carefully bounded -- 15s, Setpgid, process-group kill --
// and the function that supplies its argument was not:
//
//	exec.Command("gh", "repo", "view", ...).Output()
//
// `gh repo view` is a GraphQL round trip to api.github.com, not a local git
// read, and repoSlug runs twice on every `estate dispatch`: once inside
// sweepWorktrees, which sits AHEAD of the pressure gate, and once to build
// the dispatch worktree's own Landed seam. So it was the first
// network-dependent thing a dispatch did, with no ceiling on it, and a
// remote that drops packets rather than refusing them stopped dispatches
// returning at all. Refusing is survivable; hanging is not.
//
// These use a `gh` of our own on PATH and never touch the network.

// stubGH puts an executable `gh` running script first on PATH. First, not
// only: git and the rest stay reachable, and a real gh further down is
// shadowed for the name `gh`, which is asserted rather than assumed.
func stubGH(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("writing the gh stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got, err := exec.LookPath("gh"); err != nil || got != filepath.Join(dir, "gh") {
		t.Fatalf("the gh stub is not what would run: LookPath gave %q (%v)", got, err)
	}
	return dir
}

// The bound must actually bind. This test is itself bounded -- repoSlug runs
// on its own goroutine and the assertion is a select, never a bare call --
// because an unbounded repoSlug would otherwise hang the suite instead of
// failing it, and a test that neither passes nor fails teaches nothing.
func TestRepoSlugRefusesRatherThanHangingOnAForgeThatNeverAnswers(t *testing.T) {
	stubGH(t, `sleep 60`)
	restore := repoSlugTimeout
	repoSlugTimeout = 250 * time.Millisecond
	t.Cleanup(func() { repoSlugTimeout = restore })

	type result struct {
		slug string
		err  error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		slug, err := repoSlug()
		done <- result{slug, err}
	}()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("repoSlug answered %q from a gh that never replied", r.slug)
		}
		if r.slug != "" {
			t.Fatalf("repoSlug returned an error AND a slug %q; a caller reading the value first would act on a name it never obtained", r.slug)
		}
		if !strings.Contains(r.err.Error(), "giving up waiting is not an answer") {
			t.Fatalf("the refusal does not say the deadline stopped it: %v", r.err)
		}
		// Ten times the bound: slack enough not to be flaky, tight enough
		// that a call waiting on the stub's full 60s sleep fails. This is
		// what proves the process GROUP is killed -- bounding the direct
		// child alone leaves Output() blocking on a helper's copy of the
		// pipe, which is exactly the trap agent-estate#996's fix pass hit.
		if elapsed := time.Since(start); elapsed > 10*repoSlugTimeout {
			t.Fatalf("repoSlug took %s to honour a %s bound -- the deadline is not killing the subprocess tree", elapsed, repoSlugTimeout)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("repoSlug never returned: the gh call is unbounded, so a dispatch on a black-holed route would never return either")
	}
}

// Every ordinary failure is an error, not a guess. Both callers turn that
// into "slug unknown, so leave the Landed seam nil", which degrades to the
// pre-#1000 behaviour -- origin's branch as the only evidence, refusing when
// it cannot be read -- rather than to a wrong answer.
func TestRepoSlugRefusesWhateverGoesWrong(t *testing.T) {
	for _, c := range []struct {
		name   string
		script string // "" means: no gh on PATH at all
	}{
		{name: "gh is not installed on this host"},
		{name: "not authenticated", script: `echo 'gh: To get started with GitHub CLI, please run: gh auth login' >&2; exit 4`},
		{name: "no route to the forge", script: `echo 'dial tcp: lookup api.github.com: no such host' >&2; exit 1`},
		{name: "an answer with no name in it", script: `printf ''`},
		{name: "an answer that is only whitespace", script: `printf '   \n'`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.script == "" {
				t.Setenv("PATH", t.TempDir())
			} else {
				stubGH(t, c.script)
			}
			slug, err := repoSlug()
			if err == nil {
				t.Fatalf("repoSlug answered %q instead of refusing", slug)
			}
			if slug != "" {
				t.Fatalf("repoSlug returned both an error and the slug %q", slug)
			}
		})
	}
}

// And the answering case, so the table above cannot pass by always failing.
func TestRepoSlugReadsTheNameTheForgeGives(t *testing.T) {
	stubGH(t, `echo jonhill90/agent-estate`)
	slug, err := repoSlug()
	if err != nil {
		t.Fatalf("repoSlug: %v", err)
	}
	if slug != "jonhill90/agent-estate" {
		t.Fatalf("repoSlug read %q", slug)
	}
}
