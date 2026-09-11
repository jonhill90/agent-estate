package corpus

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A fixture corpus with the shape review.go queries: prompts (with the
// author column #1398 added) and items feeding the live_parameters view.
const reviewFixtureDDL = `
CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT,
  context TEXT NOT NULL, session TEXT, source_file TEXT, project TEXT, tmux_pane TEXT, tmux_pane_target TEXT,
  author TEXT NOT NULL DEFAULT 'unknown');
CREATE TABLE items (id TEXT PRIMARY KEY, prompt_id TEXT NOT NULL, kind TEXT NOT NULL, body TEXT NOT NULL,
  weight TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'open', status_reason TEXT, resolved_to TEXT, acked_at INTEGER);
CREATE VIEW live_parameters AS SELECT * FROM items WHERE kind = 'parameter' AND weight != 'retracted';
INSERT INTO prompts VALUES ('mp-clean', 100, 'sources shoud be valid RAWSENTINEL', 'Sources should be valid.', 'ctx', 's1', NULL, '-Users-jon-x-Hill90', NULL, NULL, 'unknown');
INSERT INTO prompts VALUES ('mp-noclean', 200, 'is it stuck again or is that a job running? RAWSENTINEL', NULL, 'ctx', 's1', NULL, '', NULL, NULL, 'unknown');
INSERT INTO prompts VALUES ('mp-secret', 300, 'the portainer password is hunter2 RAWSENTINEL', 'The Portainer password is hunter2.', 'ctx', 's2', NULL, '', NULL, NULL, 'unknown');
INSERT INTO items VALUES ('it-1', 'mp-clean', 'parameter', 'Sources must be valid; a 404 is never acceptable.', 'hard', 'acted', NULL, NULL, NULL);
INSERT INTO items VALUES ('it-2', 'mp-noclean', 'parameter', 'Distinguish a hung turn from real work before reporting status.', 'hard', 'open', NULL, NULL, NULL);
INSERT INTO items VALUES ('it-3', 'mp-noclean', 'parameter', 'Always say whether a job is running.', 'hard', 'open', NULL, NULL, NULL);
INSERT INTO items VALUES ('it-4', 'mp-secret', 'parameter', 'The Portainer password does not matter right now.', 'hard', 'acked', NULL, NULL, NULL);
INSERT INTO items VALUES ('it-5', 'mp-clean', 'directive', 'not a parameter, must not appear', 'hard', 'open', NULL, NULL, NULL);
`

func buildReviewFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corpus.sqlite3")
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(reviewFixtureDDL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 fixture setup failed: %v\n%s", err, out)
	}
	return path
}

func TestReviewGroupsLiveParametersByPromptAndCounts(t *testing.T) {
	t.Setenv("ESTATE_CORPUS", buildReviewFixture(t))
	r, err := ReviewLive()
	if err != nil {
		t.Fatal(err)
	}
	if r.Params != 4 || r.Prompts != 3 {
		t.Fatalf("Params=%d Prompts=%d, want 4 live parameters over 3 prompts (the directive must not count)", r.Params, r.Prompts)
	}
	if r.WithClean != 2 {
		t.Fatalf("WithClean=%d, want 2", r.WithClean)
	}
	if r.WithheldCount != 1 {
		t.Fatalf("WithheldCount=%d, want 1 (the password row)", r.WithheldCount)
	}
	// Most live parameters first: mp-noclean carries two.
	if r.Groups[0].PromptID != "mp-noclean" || len(r.Groups[0].Items) != 2 {
		t.Fatalf("first group = %s with %d items; want mp-noclean with 2", r.Groups[0].PromptID, len(r.Groups[0].Items))
	}
	if r.Groups[0].Author != "unknown" {
		t.Fatalf("author = %q, want the recorded value 'unknown'", r.Groups[0].Author)
	}
}

func TestPublicRenderNeverContainsRawText(t *testing.T) {
	t.Setenv("ESTATE_CORPUS", buildReviewFixture(t))
	r, err := ReviewLive()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, ReviewOptions{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "RAWSENTINEL") {
		t.Fatalf("public render leaked text_raw:\n%s", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("public render leaked a withheld row's text:\n%s", out)
	}
	if !strings.Contains(out, `Source (text_clean): "Sources should be valid."`) {
		t.Fatalf("public render must quote text_clean where it exists:\n%s", out)
	}
	if !strings.Contains(out, "cleaned excerpt unavailable") {
		t.Fatalf("public render must say when text_clean is absent rather than quote raw:\n%s", out)
	}
	if !strings.Contains(out, "Withheld: matches the credential/personal-arrangement filter (password). Items: it-4.") {
		t.Fatalf("withheld row must be listed by id with its reason:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "verdict:") {
		t.Fatalf("render must not emit a verdict column value:\n%s", out)
	}
	// JSON public render likewise carries no raw and strips withheld rows' text.
	buf.Reset()
	if err := r.Render(&buf, ReviewOptions{JSON: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "RAWSENTINEL") || strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("public JSON leaked raw or withheld text:\n%s", buf.String())
	}
}

func TestPrivateRenderShowsRawAndIsMarked(t *testing.T) {
	t.Setenv("ESTATE_CORPUS", buildReviewFixture(t))
	r, err := ReviewLive()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, ReviewOptions{Private: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "RAWSENTINEL") || !strings.Contains(out, "hunter2") {
		t.Fatalf("private render must show raw and withheld text:\n%s", out)
	}
	if !strings.Contains(out, "PRIVATE RENDER") {
		t.Fatalf("private render must be marked as such:\n%s", out)
	}
}

func TestHintsAreLabelledMeasurementsNotVerdicts(t *testing.T) {
	t.Setenv("ESTATE_CORPUS", buildReviewFixture(t))
	r, err := ReviewLive()
	if err != nil {
		t.Fatal(err)
	}
	var g ReviewGroup
	for _, x := range r.Groups {
		if x.PromptID == "mp-noclean" {
			g = x
		}
	}
	joined := strings.Join(g.Hints, "\n")
	for _, want := range []string{
		"source has no cleaned text",
		"source contains a question mark",
		"2 live parameters were judged from this one source",
		`it-3: rule uses "always"; the source does not`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("hints missing %q:\n%s", want, joined)
		}
	}
	for _, forbidden := range []string{"supported", "stronger", "invented", "made up", "verdict"} {
		if strings.Contains(strings.ToLower(joined), forbidden) {
			t.Fatalf("hint text must not carry a verdict word %q:\n%s", forbidden, joined)
		}
	}
}

func TestPagingBoundsAndTrailer(t *testing.T) {
	t.Setenv("ESTATE_CORPUS", buildReviewFixture(t))
	r, err := ReviewLive()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, ReviewOptions{Offset: 1, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Showing prompts 2-2 of 3") {
		t.Fatalf("paging header wrong:\n%s", out)
	}
	if !strings.Contains(out, "1 more prompts; rerun with --offset 2.") {
		t.Fatalf("paging trailer wrong:\n%s", out)
	}
	buf.Reset()
	if err := r.Render(&buf, ReviewOptions{Offset: 99}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Showing prompts 4-3 of 3") && !strings.Contains(buf.String(), "of 3") {
		t.Fatalf("an offset past the end must not panic and must still report the total:\n%s", buf.String())
	}
}

// Direction 1: cost vocabulary must come through. "token" in this corpus is
// almost always about spend, and withholding those rows costs the artifact
// real, legible content (145 of 970 prompts on the bare word alone).
func TestTokenCostVocabularyIsNotASecret(t *testing.T) {
	for _, s := range []string{
		"watch token usage and set a cron",
		"I dont want to waste tokens having something stop halfway",
		"we burned 2 million tokens on that",
		"keep the preamble under 16k tokens",
		"tokens per 5-hour block, then wind down",
		"we are wasting my token we could be working on that graph",
		"delegate to subagents to min/max tokens",
		// corpus ids are long alphanumerics with digits and letters; not keys
		"see it-0476148f2282ad32 and mp-8abab921cefb695d for the source",
	} {
		if got := sensitiveReason(s); got != "" {
			t.Errorf("cost vocabulary withheld: %q -> %s", s, got)
		}
	}
}

// Direction 2 (review on #1403): a real secret introduced by the bare word
// "token" -- no qualifying word, no known prefix -- must be withheld. These
// are the reviewer's exact constructed rows, plus the boundary case it named.
func TestBareTokenWithASecretValueIsWithheld(t *testing.T) {
	for _, s := range []string{
		"my token is 9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c",
		"here is the token: gh_1234567890abcdefghijklmnop",
		"token=abcdef0123456789abcdef0123456789",
		"the token is 4f8a9b2c7d1e6f3a9b2c7d1e6f3a9b2c",
		"paste the token I gave you into .env",
		"copy this token abc123DEF456ghi789 somewhere safe",
		"here's my github token ghp_abcdef1234567890",
		// secret shape with no keyword at all
		"use eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c for the call",
		"the value is 3f2a9c8b7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a",
		// existing cases
		"paste the bearer token here",
		"as soon as gone i will use my friends account",
	} {
		if got := sensitiveReason(s); got == "" {
			t.Errorf("secret passed through unwithheld: %q", s)
		}
	}
}

// Zero live parameters from a corpus that exists is blindness, not an empty
// review -- the same rule Hard() and Audit() apply.
func TestReviewRefusesEmptyAsBlindness(t *testing.T) {
	t.Setenv("ESTATE_CORPUS", t.TempDir()+"/absent.sqlite3")
	if _, err := ReviewLive(); err == nil {
		t.Fatal("ReviewLive() returned nil error for an unreadable corpus")
	}
}
