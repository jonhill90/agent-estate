package isolate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// WHY THIS FILE EXISTS (agent-estate#1009's review). Every test beside it
// drives Remove's decision logic through a FAKE Landed seam, which is the
// right way to test a decision -- but it left GHLanded, the only Landed that
// ever runs in production, with its subprocess-result path untested. The
// mutation that proves the gap:
//
//	out, err := cmd.CombinedOutput()
//	if err != nil {
//		return true, nil        // gh missing, 404, 422, auth, rate limit, timeout
//	}
//
// That reads every failure to ask as "the work landed", which authorises
// deleting the only copy of a turn's output. It survived the whole suite.
// The tests here kill it: each names a way asking can fail, and each
// requires a REFUSAL -- an error, and landed=false -- rather than an answer.
//
// No network is used. A `gh` of our own goes on PATH, so these run anywhere,
// including a runner with no credentials and no route to github.com. That is
// deliberate: a test for what happens when the forge cannot be reached must
// not itself need the forge to be reachable.

// stubGH writes an executable `gh` running script into a fresh directory and
// puts that directory FIRST on PATH, so our gh shadows any real one while
// git -- which Remove needs on the same path -- is still found. It returns
// the directory. Any real gh further down the path is unreachable for the
// name `gh`, so no test here can accidentally touch the live forge.
func stubGH(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("writing the gh stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Prove the shadow actually took, rather than trusting PATH order: a
	// test that silently reached the real gh would be a network call
	// pretending to be a fixture.
	if got, err := exec.LookPath("gh"); err != nil || got != filepath.Join(dir, "gh") {
		t.Fatalf("the gh stub is not what would run: LookPath gave %q (%v)", got, err)
	}
	return dir
}

// theCommit is a real object name, so safeCommit passes and the subprocess
// is actually reached -- which is the whole point of this file.
const theCommit = "20e5adc2c762f2ff0c5ec04f83a864a26e1332d3"

// Every way asking can fail must refuse. None of these may return "landed".
func TestGHLandedRefusesEveryFailureToAsk(t *testing.T) {
	for _, c := range []struct {
		name   string
		script string // "" means: no gh on PATH at all
	}{
		{
			name: "gh is not installed on this host",
		},
		{
			name:   "not authenticated",
			script: `echo 'gh: To get started with GitHub CLI, please run: gh auth login' >&2; exit 4`,
		},
		{
			name:   "rate limited",
			script: `echo 'gh: API rate limit exceeded for user ID 1. (HTTP 403)' >&2; exit 1`,
		},
		{
			name:   "the commit is unknown to the forge",
			script: `echo 'gh: No commit found for SHA: ' >&2; exit 1`,
		},
		{
			name:   "the repository is gone, transferred or renamed",
			script: `echo 'gh: Not Found (HTTP 404)' >&2; exit 1`,
		},
		{
			name:   "no route to the forge at all",
			script: `echo 'dial tcp: lookup api.github.com: no such host' >&2; exit 1`,
		},
		{
			name:   "something answered, but not with a number",
			script: `echo '<html><title>502 Proxy Error</title></html>'`,
		},
		{
			name:   "something answered with nothing at all",
			script: `printf ''`,
		},
		{
			name:   "the shape changed and it is a body rather than a count",
			script: `echo '[{"number":996,"merged_at":"2026-09-03T19:58:39Z"}]'`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.script == "" {
				t.Setenv("PATH", t.TempDir()) // an empty PATH: no gh anywhere
			} else {
				stubGH(t, c.script)
			}
			landed, err := GHLanded("jonhill90/agent-estate")(theCommit)
			if err == nil {
				t.Fatalf("GHLanded answered landed=%v instead of refusing -- a failure to ask is not an answer", landed)
			}
			if landed {
				t.Fatal("GHLanded returned an error AND landed=true; a caller reading the bool first would delete the only copy of a turn's work")
			}
		})
	}
}

// Giving up waiting is not an answer either. The bound has to actually bind:
// this is the failure that hangs teardown rather than refusing it, and it is
// the one a plain `if err != nil { return true, nil }` mutation reads as
// "landed" most quietly, because nothing ever prints.
func TestGHLandedRefusesRatherThanWaitingForever(t *testing.T) {
	stubGH(t, `sleep 60`)
	restore := ghTimeout
	ghTimeout = 250 * time.Millisecond
	t.Cleanup(func() { ghTimeout = restore })

	start := time.Now()
	landed, err := GHLanded("jonhill90/agent-estate")(theCommit)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("a timeout was read as an answer: landed=%v err=%v", landed, err)
	}
	if landed {
		t.Fatal("GHLanded reported landed=true on a call that never returned an answer")
	}
	if !strings.Contains(err.Error(), "giving up waiting is not an answer") {
		t.Fatalf("the refusal does not say it was the deadline that stopped it: %v", err)
	}
	// The bound binds on the process GROUP, not just the direct child: a
	// helper holding the output pipe open would leave CombinedOutput
	// blocking long past the deadline. Ten times the bound is slack enough
	// not to be flaky and tight enough that an unbounded wait (60s) fails.
	if elapsed > 10*ghTimeout {
		t.Fatalf("GHLanded took %s to honour a %s bound -- the deadline is not actually killing the subprocess tree", elapsed, ghTimeout)
	}
}

// The question asked has to be the question described: this repository, this
// commit, and a filter that counts only pull requests that MERGED. A stub
// answering "1" to whatever it is asked would let the endpoint drift without
// any test noticing.
func TestGHLandedAsksTheForgeTheDocumentedQuestion(t *testing.T) {
	dir := stubGH(t, `printf '%s\n' "$@" > "$GHSTUB_ARGV"; echo 1`)
	argv := filepath.Join(dir, "argv")
	t.Setenv("GHSTUB_ARGV", argv)

	if _, err := GHLanded("jonhill90/agent-estate")(theCommit); err != nil {
		t.Fatalf("GHLanded: %v", err)
	}
	b, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("the stub was never run: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{
		"api",
		"repos/jonhill90/agent-estate/commits/" + theCommit + "/pulls",
		"--jq",
		"[.[] | select(.merged_at != null)] | length",
	}
	if len(got) != len(want) {
		t.Fatalf("gh was called with %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("gh argument %d was %q, want %q", i, got[i], want[i])
		}
	}
}

// The wrong-ANSWER cases, as opposed to the cannot-ask ones above: a commit
// the forge knows about but that nothing has merged. These are the shapes
// that must come back "not landed" so that Remove keeps refusing.
//
// The stub runs the REAL --jq filter GHLanded passes over real response
// bodies, rather than hardcoding the number the filter would produce -- so
// this tests the filter, not a restatement of it. gh evaluates --jq
// in-process, so a jq binary stands in for it here; without one there is
// nothing honest to run and the test says so rather than passing.
func TestGHLandedReportsNotLandedForEveryUnmergedShape(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("no jq on this host, so the real --jq filter cannot be evaluated without gh itself")
	}
	for _, c := range []struct {
		name string
		body string
		want bool
	}{
		{
			name: "the commit belongs to no pull request at all",
			body: `[]`,
		},
		{
			name: "the commit heads an OPEN pull request",
			body: `[{"number":1009,"state":"open","merged_at":null}]`,
		},
		{
			name: "the pull request was CLOSED without merging",
			body: `[{"number":989,"state":"closed","merged_at":null}]`,
		},
		{
			name: "every pull request containing it was closed unmerged",
			body: `[{"number":986,"state":"closed","merged_at":null},{"number":989,"state":"closed","merged_at":null}]`,
		},
		{
			// A fork's own endpoint does not report the upstream merge, so
			// asking it yields no merged pull request. Refusing is the
			// correct direction: the fork's worktree holds work this
			// repository has no record of collecting.
			name: "a fork, whose endpoint reports nothing merged here",
			body: `[]`,
		},
		{
			// The one shape that authorises removal, kept in the same table
			// so the table cannot pass by always saying "no".
			name: "merged, which is the only yes",
			body: `[{"number":996,"state":"closed","merged_at":"2026-09-03T19:58:39Z"}]`,
			want: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := stubGH(t, `exec `+jq+` "$4" "$GHSTUB_BODY"`)
			body := filepath.Join(dir, "body.json")
			if err := os.WriteFile(body, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GHSTUB_BODY", body)

			landed, err := GHLanded("jonhill90/agent-estate")(theCommit)
			if err != nil {
				t.Fatalf("GHLanded could not read a well-formed answer: %v", err)
			}
			if landed != c.want {
				t.Fatalf("GHLanded said landed=%v, want %v, for %s", landed, c.want, c.body)
			}
		})
	}
}

// End to end through the real seam rather than a fake one: a squash-merged
// worktree, GHLanded as its actual Landed, and a gh that can or cannot
// answer. This is what joins the contract above to the behaviour that
// destroys a directory.
func TestRemoveUsesTheRealGHLandedInBothDirections(t *testing.T) {
	t.Run("a forge that says merged releases the worktree", func(t *testing.T) {
		w := squashMerged(t, "real-gh-landed")
		stubGH(t, `echo 1`)
		w.Landed = GHLanded("jonhill90/agent-estate")
		if err := w.Remove(); err != nil {
			t.Fatalf("Remove refused work the forge says landed: %v", err)
		}
		if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
			t.Fatalf("Remove reported success but %s is still there", w.Path)
		}
	})
	t.Run("a forge that cannot be asked keeps it", func(t *testing.T) {
		w := squashMerged(t, "real-gh-unreachable")
		stubGH(t, `echo 'gh: Not Found (HTTP 404)' >&2; exit 1`)
		w.Landed = GHLanded("jonhill90/agent-estate")
		if err := w.Remove(); err == nil {
			t.Fatal("Remove destroyed a turn's only output on evidence it never obtained")
		}
		if _, err := os.Stat(filepath.Join(w.Path, "result.txt")); err != nil {
			t.Fatalf("the refused Remove destroyed the turn's output anyway: %v", err)
		}
	})
}
