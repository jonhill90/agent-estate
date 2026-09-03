package main

// This file supplies the real implementation of tick.Resolve --
// agent-estate#931's fix. internal/tick stays a pure reader/writer with no
// network or os/exec knowledge (same discipline as Record's own `produced`
// callback); the actual HTTP request and `gh` calls live here, next to it.
//
// Every external call is reached through a small function-typed seam
// (httpStatus, ghAPI) so resolveURL -- the exact path agent-estate#931's two
// fabricated artifacts went through -- is unit-testable with fakes instead
// of a real network connection or a real GitHub token.

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/tick"
)

// httpStatus performs one request against a URL and returns its status
// code. err is set only when the request could not be completed at all
// (DNS failure, connection refused, timeout) -- never alongside a status
// code, so a caller can tell "reached the server and got a code" from
// "never reached the server" without inspecting the code itself.
type httpStatus func(url string) (int, error)

// ghAPI runs `gh api <path>` and reports which of three things happened:
// the call succeeded (the thing exists), it failed with a confirmed 404
// (the thing does not exist), or it failed some other way (gh could not be
// asked at all -- no auth, no network, gh missing).
type ghAPI func(path string) (exists bool, notFound bool, err error)

// defaultHTTPStatus is the real implementation: a HEAD request, falling
// back to GET when the server rejects HEAD (405) or refuses it outright --
// some servers do not implement HEAD at all.
func defaultHTTPStatus(u string) (int, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	get := func() (int, error) {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return resp.StatusCode, nil
	}
	req, err := http.NewRequest(http.MethodHead, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return get()
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed {
		return get()
	}
	return resp.StatusCode, nil
}

// defaultGHAPI is the real implementation, shelling to `gh api` (this repo's
// cli_first convention -- see AGENTS.md's Operator Parameters).
func defaultGHAPI(path string) (exists bool, notFound bool, err error) {
	out, runErr := exec.Command("gh", "api", path).CombinedOutput()
	if runErr == nil {
		return true, false, nil
	}
	msg := strings.ToLower(string(out))
	if strings.Contains(msg, "404") || strings.Contains(msg, "not found") {
		return false, true, nil
	}
	return false, false, fmt.Errorf("gh api %s: %s", path, strings.TrimSpace(string(out)))
}

// githubCommentRE names a GitHub issue-or-PR comment by owner, repo, number
// and comment id -- the exact shape of both fabricated artifacts in
// agent-estate#931.
var githubCommentRE = regexp.MustCompile(`github\.com/([^/\s]+)/([^/\s]+)/(?:issues|pull)/(\d+)#issuecomment-(\d+)`)

// shaLikeRE matches a bare candidate token that looks like a commit sha,
// distinguishing it from a path in resolveToken.
var shaLikeRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// newResolver builds a tick.Resolve over the given seams -- production code
// calls it with the real implementations; tests call it with fakes.
func newResolver(doHTTP httpStatus, doGH ghAPI) tick.Resolve {
	return func(artifact, srcHead string) (tick.Resolution, string) {
		return resolveArtifact(artifact, srcHead, doHTTP, doGH)
	}
}

// resolveArtifact is the entry point: a URL gets a real request and a real
// status (agent-estate#931's own words); anything else is treated as prose
// that may contain a path, a commit sha, or an issue/PR reference, and each
// candidate token is checked in turn.
func resolveArtifact(artifact, srcHead string, doHTTP httpStatus, doGH ghAPI) (tick.Resolution, string) {
	a := strings.TrimSpace(artifact)
	if a == "" {
		return tick.ResolveValid, "no artifact to check"
	}
	if strings.Contains(a, "://") {
		return resolveURL(a, doHTTP, doGH)
	}
	return resolveProse(a, srcHead)
}

// resolveURL is the fix for agent-estate#931's two fabricated entries.
// Validate's own "://" shortcut accepts any URL on shape alone; this asks
// the source instead.
//
// A GitHub issue/PR comment URL gets a `gh api` call against the specific
// comment id, because the URL's #issuecomment-N fragment is never sent to
// the server -- an HTTP request to the bare issue/PR page returns 200
// whether or not that comment exists, so status-code checking alone would
// have passed both fabricated artifacts in agent-estate#931 (the issue
// #940 they were attached to is real; only the comment id was invented).
// Any other URL gets a plain request and its status code.
func resolveURL(u string, doHTTP httpStatus, doGH ghAPI) (tick.Resolution, string) {
	if m := githubCommentRE.FindStringSubmatch(u); m != nil {
		owner, repo, commentID := m[1], m[2], m[4]
		exists, notFound, err := doGH(fmt.Sprintf("repos/%s/%s/issues/comments/%s", owner, repo, commentID))
		switch {
		case err != nil:
			return tick.ResolveUnknown, "gh api could not check comment " + commentID + ": " + err.Error()
		case notFound:
			return tick.ResolveInvalid, fmt.Sprintf("comment %s does not exist: %s", commentID, u)
		case exists:
			return tick.ResolveValid, "comment " + commentID + " exists: " + u
		default:
			return tick.ResolveUnknown, "gh api returned neither success nor a confirmed 404 for " + u
		}
	}
	status, err := doHTTP(u)
	if err != nil {
		return tick.ResolveUnknown, "could not reach " + u + ": " + err.Error()
	}
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return tick.ResolveInvalid, fmt.Sprintf("%s returned %d", u, status)
	case status >= 200 && status < 400:
		return tick.ResolveValid, fmt.Sprintf("%s returned %d", u, status)
	default:
		return tick.ResolveUnknown, fmt.Sprintf("%s returned %d, which is neither confirmation nor a confirmed 404", u, status)
	}
}

// resolveProse checks each path/sha/issue-or-PR candidate token Validate
// would already have extracted, and accepts if any one of them resolves.
// This mirrors Validate's own tolerance for prose ("fixed the thing in
// docs/phase-plan.md") rather than requiring the whole artifact to be one
// bare token.
func resolveProse(a, srcHead string) (tick.Resolution, string) {
	cands := tick.Candidates(a)
	if len(cands) == 0 {
		return tick.ResolveInvalid, "names nothing checkable"
	}
	sawUnknown := false
	for _, c := range cands {
		switch res, detail := resolveToken(c, srcHead); res {
		case tick.ResolveValid:
			return res, detail
		case tick.ResolveUnknown:
			sawUnknown = true
		}
	}
	if sawUnknown {
		return tick.ResolveUnknown, "could not confirm any token in " + strconv.Quote(a)
	}
	return tick.ResolveInvalid, "none of the tokens in " + strconv.Quote(a) + " exist"
}

// resolveToken checks one candidate token: a path against the repository as
// of HEAD (falling back to the disk for something not yet committed), a sha
// against the repository, or an issue/PR number against gh.
//
// srcHead is accepted (it is part of Resolve's signature, and Record's own
// artifact-recency check is keyed on it) but deliberately NOT used to pin a
// path's existence, and that is a correction made against real evidence, not
// the literal reading of agent-estate#931's own wording ("the file exists at
// the recorded src_head"). Running this resolver against the real
// docs/tick-log.jsonl (see the PR description for the exact counts) showed
// why: src_head is `git log -1 --format=%H -- src/` -- the last commit that
// touched src/, which is unrelated to whether a docs/ artifact exists, and
// even for a src/ artifact it is typically the commit BEFORE the one that
// created it (Validate's whole recency rule is that the artifact postdates
// src_head, not that it is present in it). Pinning existence to src_head
// therefore reported real, currently-existing files as invalid. Checking
// against HEAD -- "does this exist now" -- is what the evidence supports.
func resolveToken(tok, _ string) (tick.Resolution, string) {
	switch {
	case strings.HasPrefix(tok, "#"):
		num := strings.TrimPrefix(tok, "#")
		prRes, prDetail := ghRefExists("pr", num)
		if prRes == tick.ResolveValid {
			return prRes, prDetail
		}
		issueRes, issueDetail := ghRefExists("issue", num)
		if issueRes == tick.ResolveValid {
			return issueRes, issueDetail
		}
		if prRes == tick.ResolveUnknown || issueRes == tick.ResolveUnknown {
			return tick.ResolveUnknown, "gh could not confirm or deny " + tok
		}
		return tick.ResolveInvalid, tok + " is neither a real PR nor a real issue"
	case shaLikeRE.MatchString(tok):
		if err := exec.Command("git", "cat-file", "-e", tok).Run(); err == nil {
			return tick.ResolveValid, tok + " is a real commit"
		}
		return tick.ResolveInvalid, tok + " is not a commit in this repository"
	default:
		if err := exec.Command("git", "cat-file", "-e", "HEAD:"+tok).Run(); err == nil {
			return tick.ResolveValid, tok + " exists in the repository (HEAD)"
		}
		// Not every real path names something tracked in THIS repo --
		// "~/.local/state/estate/ledger.jsonl" turned up in the real
		// docs/tick-log.jsonl naming a genuine file outside it. os.Stat does
		// not expand "~" itself (that is a shell convention, not a
		// filesystem one), so do it here rather than report a real file
		// invalid because of an unexpanded tilde.
		if _, err := os.Stat(expandHome(tok)); err == nil {
			return tick.ResolveValid, tok + " exists on disk"
		}
		return tick.ResolveInvalid, tok + " does not exist in the repository or on disk"
	}
}

// expandHome replaces a leading "~" with the user's home directory. Any
// failure to determine it (no HOME set, os/user unavailable) leaves tok
// unchanged rather than guessing -- the subsequent os.Stat then fails
// honestly instead of on a fabricated path.
func expandHome(tok string) string {
	if tok != "~" && !strings.HasPrefix(tok, "~/") {
		return tok
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return tok
	}
	if tok == "~" {
		return home
	}
	return filepath.Join(home, tok[2:])
}

// ghRefExists asks whether num is a real PR or issue (per kind). A gh
// failure is Invalid only when gh itself says so (a resolvable "not found");
// any other failure (no auth, no network, gh missing) is Unknown.
func ghRefExists(kind, num string) (tick.Resolution, string) {
	out, err := exec.Command("gh", kind, "view", num, "--json", "number").CombinedOutput()
	if err == nil {
		return tick.ResolveValid, "#" + num + " is a real " + kind
	}
	msg := strings.ToLower(string(out))
	if strings.Contains(msg, "not found") || strings.Contains(msg, "could not resolve") {
		return tick.ResolveInvalid, "#" + num + " does not exist as a " + kind
	}
	return tick.ResolveUnknown, "gh could not check #" + num + " as a " + kind + ": " + strings.TrimSpace(string(out))
}
