package main

// agent-estate#931: `estate tick record` accepted two fabricated artifacts in
// one session, both URLs, both passed on shape alone:
//
//	https://github.com/jonhill90/agent-estate/pull/926#issuecomment-latest
//	https://github.com/jonhill90/agent-estate/issues/940#issuecomment-5523579200
//
// The second is the important one: a plausible 10-digit comment id, differing
// from the real one (5523608412) only in its digits. Nothing about it looks
// wrong to a syntactic check or to a human skimming the log. These tests
// exercise resolveURL directly with fakes standing in for gh and HTTP, so
// they run without a network connection or a real GitHub token, and they use
// both real examples from the issue verbatim.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/tick"
)

const (
	fabricatedPlaceholderURL = "https://github.com/jonhill90/agent-estate/pull/926#issuecomment-latest"
	fabricatedPlausibleURL   = "https://github.com/jonhill90/agent-estate/issues/940#issuecomment-5523579200"
	genuineCommentURL        = "https://github.com/jonhill90/agent-estate/issues/940#issuecomment-5523608412"
)

// fakeGH returns a ghAPI that answers only for the comment ids in exists,
// treating everything else as a confirmed 404 -- standing in for a live
// GitHub repository where exactly one of these three comment ids is real.
func fakeGH(exists map[string]bool) ghAPI {
	return func(path string) (bool, bool, error) {
		for id, ok := range exists {
			if path == "repos/jonhill90/agent-estate/issues/comments/"+id {
				if ok {
					return true, false, nil
				}
				return false, true, nil
			}
		}
		return false, true, nil // unlisted id: confirmed absent
	}
}

func neverCalledHTTP(t *testing.T) httpStatus {
	return func(u string) (int, error) {
		t.Fatalf("resolveURL should have used the gh comment path, not a plain HTTP request, for %s", u)
		return 0, nil
	}
}

// Test 1: a well-formed but non-existent artifact is not accepted as valid --
// using both real fabricated examples from the issue.
func TestFabricatedArtifactsDoNotResolve(t *testing.T) {
	gh := fakeGH(map[string]bool{"5523608412": true}) // only the real comment exists

	for _, u := range []string{fabricatedPlausibleURL} {
		res, detail := resolveURL(u, neverCalledHTTP(t), gh)
		if res != tick.ResolveInvalid {
			t.Errorf("resolveURL(%q) = %s (%s), want invalid -- this is the plausible-but-fake id from agent-estate#931", u, res, detail)
		}
	}

	// The placeholder anchor ("#issuecomment-latest") is not even shaped like
	// a GitHub comment URL -- githubCommentRE requires digits -- so it falls
	// through to a plain HTTP request. The GitHub issue page it names is
	// real, so a status-only check would wrongly pass it; that is exactly
	// why the comment-specific path exists for the digit-anchor case above,
	// and this asserts the placeholder is at least not silently ResolveValid
	// via the comment-verification path (it is not a match for that regex at
	// all, so it must fall to the generic URL path, not be treated as a
	// verified comment).
	if m := githubCommentRE.FindStringSubmatch(fabricatedPlaceholderURL); m != nil {
		t.Fatalf("expected %q not to match the comment-id pattern (it has no digit anchor); got %v", fabricatedPlaceholderURL, m)
	}
}

// Test 2: a genuine artifact is accepted.
func TestGenuineArtifactResolves(t *testing.T) {
	gh := fakeGH(map[string]bool{"5523608412": true})
	res, detail := resolveURL(genuineCommentURL, neverCalledHTTP(t), gh)
	if res != tick.ResolveValid {
		t.Fatalf("resolveURL(%q) = %s (%s), want valid -- this is the real comment id from agent-estate#931", genuineCommentURL, res, detail)
	}
}

// Test 3: "could not check" is distinguishable from both valid and invalid.
func TestNetworkFailureIsUnknownNotInvalidNotValid(t *testing.T) {
	brokenGH := func(path string) (bool, bool, error) {
		return false, false, errors.New("gh: authentication required")
	}
	res, detail := resolveURL(genuineCommentURL, neverCalledHTTP(t), brokenGH)
	if res != tick.ResolveUnknown {
		t.Fatalf("resolveURL with an unreachable gh = %s (%s), want unknown -- a failed check must not read as either confirmed or fabricated", res, detail)
	}
	if res == tick.ResolveValid || res == tick.ResolveInvalid {
		t.Fatal("unknown must be its own value, never coerced to valid or invalid")
	}

	// The same distinction for a plain (non-comment) URL whose request
	// cannot even be completed.
	unreachableHTTP := func(u string) (int, error) { return 0, errors.New("dial tcp: no route to host") }
	res2, _ := resolveURL("https://example.com/report.md", unreachableHTTP, fakeGH(nil))
	if res2 != tick.ResolveUnknown {
		t.Fatalf("an unreachable plain URL must resolve unknown, got %s", res2)
	}

	// And the positive control: a real, reachable URL still resolves valid,
	// so unknown is not the resolver silently refusing to try.
	okHTTP := func(u string) (int, error) { return 200, nil }
	res3, _ := resolveURL("https://example.com/report.md", okHTTP, fakeGH(nil))
	if res3 != tick.ResolveValid {
		t.Fatalf("a reachable URL returning 200 must resolve valid, got %s", res3)
	}

	// And a confirmed-absent plain URL resolves invalid, not unknown.
	notFoundHTTP := func(u string) (int, error) { return 404, nil }
	res4, _ := resolveURL("https://example.com/gone.md", notFoundHTTP, fakeGH(nil))
	if res4 != tick.ResolveInvalid {
		t.Fatalf("a 404 must resolve invalid, got %s", res4)
	}
}

// CheckWithResolver end to end: the three-state resolver actually changes
// the stall verdict, using the same fabricated/genuine pair from the issue.
func TestCheckWithResolverDoesNotLetAFabricatedArtifactClearAStall(t *testing.T) {
	log := writeTickLog(t,
		`{"at":"2026-09-03T09:20:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-03T09:24:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-03T09:28:55Z","phase_item":"phase-0","src_head":"aaa","artifact":"`+fabricatedPlausibleURL+`"}`,
	)
	resolve := newResolver(neverCalledHTTP(t), fakeGH(map[string]bool{"5523608412": true}))
	v, err := tick.CheckWithResolver(log, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Stalled {
		t.Fatalf("a fabricated artifact must not clear the stall once resolved: %s", v.Reason)
	}
	if v.Unverifiable != 0 {
		t.Fatalf("a confirmed-invalid artifact is not the same as unverifiable: %+v", v)
	}
}

func TestCheckWithResolverAcceptsAGenuineArtifact(t *testing.T) {
	log := writeTickLog(t,
		`{"at":"2026-09-03T09:20:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-03T09:24:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-03T09:29:02Z","phase_item":"phase-0","src_head":"aaa","artifact":"`+genuineCommentURL+`"}`,
	)
	resolve := newResolver(neverCalledHTTP(t), fakeGH(map[string]bool{"5523608412": true}))
	v, err := tick.CheckWithResolver(log, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if v.Stalled {
		t.Fatalf("a genuine, resolved artifact must clear the stall: %s", v.Reason)
	}
}

// The typed third state at the Verdict level: an unresolvable artifact does
// not clear the stall (it is not treated as valid) but is reported
// separately from a confirmed-fabricated one (it is not treated as a
// failure either) via Verdict.Unverifiable.
func TestCheckWithResolverReportsUnverifiableSeparatelyFromInvalid(t *testing.T) {
	log := writeTickLog(t,
		`{"at":"2026-09-03T09:20:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-03T09:24:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-03T09:29:02Z","phase_item":"phase-0","src_head":"aaa","artifact":"`+genuineCommentURL+`"}`,
	)
	broken := func(path string) (bool, bool, error) { return false, false, errors.New("no network") }
	resolve := newResolver(neverCalledHTTP(t), broken)
	v, err := tick.CheckWithResolver(log, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Stalled {
		t.Fatalf("an unverifiable artifact must not clear the stall either: %s", v.Reason)
	}
	if v.Unverifiable != 1 {
		t.Fatalf("Unverifiable = %d, want 1 -- a network failure must be visible as its own count, not silently folded into the stall reason as fabrication", v.Unverifiable)
	}
}

func writeTickLog(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tick-log.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
