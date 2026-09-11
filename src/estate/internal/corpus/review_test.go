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
// Every piece of text carries a sentinel so a leak into the public render
// is caught by name: RAWSENTINEL (text_raw), CLEANSENTINEL (text_clean),
// BODYSENTINEL (a rule body), hunter2 (a secret in a flagged row).
const reviewFixtureDDL = `
CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT,
  context TEXT NOT NULL, session TEXT, source_file TEXT, project TEXT, tmux_pane TEXT, tmux_pane_target TEXT,
  author TEXT NOT NULL DEFAULT 'unknown');
CREATE TABLE items (id TEXT PRIMARY KEY, prompt_id TEXT NOT NULL, kind TEXT NOT NULL, body TEXT NOT NULL,
  weight TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'open', status_reason TEXT, resolved_to TEXT, acked_at INTEGER);
CREATE VIEW live_parameters AS SELECT * FROM items WHERE kind = 'parameter' AND weight != 'retracted';
INSERT INTO prompts VALUES ('mp-clean', 100, 'sources shoud be valid RAWSENTINEL', 'Sources should be valid CLEANSENTINEL.', 'ctx', 's1', NULL, '-Users-jon-x-Hill90', NULL, NULL, 'unknown');
INSERT INTO prompts VALUES ('mp-noclean', 200, 'is it stuck again or is that a job running? RAWSENTINEL', NULL, 'ctx', 's1', NULL, '', NULL, NULL, 'unknown');
INSERT INTO prompts VALUES ('mp-secret', 300, 'the portainer password is hunter2 RAWSENTINEL', 'The Portainer password is hunter2.', 'ctx', 's2', NULL, '', NULL, NULL, 'unknown');
INSERT INTO items VALUES ('it-1', 'mp-clean', 'parameter', 'Sources must be valid BODYSENTINEL; a 404 is never acceptable.', 'hard', 'acted', NULL, NULL, NULL);
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

func loadFixtureReview(t *testing.T) *Review {
	t.Helper()
	t.Setenv("ESTATE_CORPUS", buildReviewFixture(t))
	r, err := ReviewLive()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReviewGroupsLiveParametersByPromptAndCounts(t *testing.T) {
	r := loadFixtureReview(t)
	if r.Params != 4 || r.Prompts != 3 {
		t.Fatalf("Params=%d Prompts=%d, want 4 live parameters over 3 prompts (the directive must not count)", r.Params, r.Prompts)
	}
	if r.WithClean != 2 || r.ParamsClean != 2 {
		t.Fatalf("WithClean=%d ParamsClean=%d, want 2 and 2", r.WithClean, r.ParamsClean)
	}
	if r.AdvisoryCount != 1 {
		t.Fatalf("AdvisoryCount=%d, want 1 (the password row)", r.AdvisoryCount)
	}
	// Most live parameters first: mp-noclean carries two.
	if r.Groups[0].PromptID != "mp-noclean" || len(r.Groups[0].Items) != 2 {
		t.Fatalf("first group = %s with %d items; want mp-noclean with 2", r.Groups[0].PromptID, len(r.Groups[0].Items))
	}
	if r.Groups[0].Author != "unknown" {
		t.Fatalf("author = %q, want the recorded value 'unknown'", r.Groups[0].Author)
	}
}

// The public render is structure only. No sentinel from any text column may
// appear, in markdown or JSON, and the advisory (whose reason can name what
// it matched) stays private too. This is the property that makes the public
// artifact safe by construction rather than by filter.
func TestPublicRenderCarriesNoText(t *testing.T) {
	r := loadFixtureReview(t)
	for _, opts := range []ReviewOptions{{}, {JSON: true}} {
		var buf bytes.Buffer
		if err := r.Render(&buf, opts); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		for _, leak := range []string{"RAWSENTINEL", "CLEANSENTINEL", "BODYSENTINEL", "hunter2", "Distinguish a hung turn", "credential/personal-arrangement filter", "password"} {
			if strings.Contains(out, leak) {
				t.Fatalf("public render (json=%v) leaked %q:\n%s", opts.JSON, leak, out)
			}
		}
		for _, want := range []string{"it-1", "it-2", "it-3", "it-4", "mp-clean", "mp-noclean", "mp-secret"} {
			if !strings.Contains(out, want) {
				t.Fatalf("public render (json=%v) must carry the ids; missing %q:\n%s", opts.JSON, want, out)
			}
		}
		if strings.Contains(strings.ToLower(out), "verdict:") {
			t.Fatalf("render must not emit a verdict:\n%s", out)
		}
	}
	var buf bytes.Buffer
	_ = r.Render(&buf, ReviewOptions{})
	if !strings.Contains(buf.String(), "ids and structure only") {
		t.Fatalf("public markdown must say what it is:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "cleaned text exists (") || !strings.Contains(buf.String(), "no cleaned text (raw length") {
		t.Fatalf("public markdown must say whether cleaned text exists without quoting it:\n%s", buf.String())
	}
}

// The private render carries everything and marks the row a human must not
// lift into a public place, with the reason.
func TestPrivateRenderShowsEverythingAndAdvises(t *testing.T) {
	r := loadFixtureReview(t)
	var buf bytes.Buffer
	if err := r.Render(&buf, ReviewOptions{Private: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"RAWSENTINEL", "CLEANSENTINEL", "BODYSENTINEL", "hunter2", "PRIVATE RENDER",
		"DO NOT QUOTE PUBLICLY: matches the credential/personal-arrangement filter (password).", "1 prompts carry a do-not-quote advisory"} {
		if !strings.Contains(out, want) {
			t.Fatalf("private render missing %q:\n%s", want, out)
		}
	}
	buf.Reset()
	if err := r.Render(&buf, ReviewOptions{Private: true, JSON: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "BODYSENTINEL") || !strings.Contains(buf.String(), `"do_not_quote"`) {
		t.Fatalf("private JSON must carry bodies and the advisory:\n%s", buf.String())
	}
}

func TestHintsAreLabelledMeasurementsNotVerdicts(t *testing.T) {
	r := loadFixtureReview(t)
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
	r := loadFixtureReview(t)
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
	if !strings.Contains(buf.String(), "of 3") {
		t.Fatalf("an offset past the end must not panic and must still report the total:\n%s", buf.String())
	}
}

// --- the advisory flag, both directions ---
//
// The flag advises the human who quotes by hand; it gates nothing (the
// public render carries no text). Direction 1: cost vocabulary and ordinary
// identifiers must not be flagged, or the advisory becomes noise.
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
		// second review on #1403: hyphenated deployment names and paths mix
		// digits and letters segment by segment and are not keys
		"audit-hill90-ui-client rollback of the observability-alerts change",
		"the tree at Users-jon-source-repos-Personal-Hill90 is the one to read",
		"roll back deploy-2026-09-11-hill90-app-v2 before the next tick",
		// third review on #1403: CamelCase identifiers are this corpus's own
		// vocabulary (14 of 970 prompts carry one 20+ chars long)
		"TempoIngestionErrors fired and createBoundedSseWriter is the fix",
		"ScheduledWorkflowSignalMissing is the alert to keep",
	} {
		if got := sensitiveReason(s); got != "" {
			t.Errorf("cost vocabulary or ordinary identifier flagged: %q -> %s", s, got)
		}
	}
}

// Direction 2: the attack rows from all three reviews on #1403 must be
// flagged. Round 1: bare "token" with a value. Round 2: single-case keys of
// any length, "creds". Round 3: alphabetic passphrases jammed together.
func TestBareTokenWithASecretValueIsFlagged(t *testing.T) {
	for _, s := range []string{
		"my token is 9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c",
		"here is the token: gh_1234567890abcdefghijklmnop",
		"token=abcdef0123456789abcdef0123456789",
		"the token is 4f8a9b2c7d1e6f3a9b2c7d1e6f3a9b2c",
		"paste the token I gave you into .env",
		"copy this token abc123DEF456ghi789 somewhere safe",
		"here's my github token ghp_abcdef1234567890",
		"use eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c for the call",
		"the value is 3f2a9c8b7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a",
		"paste this in: gskq29fh3nvmzax7wlpe9k3jf7h2mzq8x1v5n0c4b6d8e2a1f9c7b3d5e0a2f4c6b8",
		"the api response included z9x8w7v6u5t4s3r2q1p0o9n8m7l6k5j4i3h2g1f0e9d8c7b6a5",
		"here is the value you need gskq29fh3nvmzax7wlpe for the deploy",
		"here are the creds: user and pass in the vault",
		"I also need an ssh key so you can rebuild the vps",
		"head is 08f76bcfaaf06ae332fb2d3af1aef6c2f82509aa on main", // a commit SHA reads as a hex run: accepted
		"unlock with correcthorsebatterystaple please",
		"the recovery phrase is mangothunderpurplevelvetocean for the wallet",
		"ssh into it with thequickbrownfoxjumpsoverthelazydog as the passcode",
		"paste the bearer token here",
		"as soon as gone i will use my friends account",
	} {
		if got := sensitiveReason(s); got == "" {
			t.Errorf("secret passed the advisory unflagged: %q", s)
		}
	}
}

// The known misses, pinned so they stay named rather than drifting. If a
// change closes one, update review.go's comment and flip the case here.
func TestAdvisoryKnownMissesAreTheOnesNamed(t *testing.T) {
	for _, s := range []string{
		"use ab12cd34ef56gh78 to log in",             // under the 20-char floor
		"correct horse battery staple is the phrase", // multi-word passphrase
		"unlock with CorrectHorseBatteryStaple",      // CamelCase passphrase
	} {
		if got := sensitiveReason(s); got != "" {
			t.Errorf("a named miss is now flagged (%s) -- update review.go's comment: %q", got, s)
		}
	}
	if got := sensitiveReason("use ab12cd34ef56gh78ij90 to log in"); got == "" {
		t.Fatal("the 20-character floor moved: a 20-char keyword-free value passed")
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
