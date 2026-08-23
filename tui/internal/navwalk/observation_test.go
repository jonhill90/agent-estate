package navwalk

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAppendObservationThenReadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.jsonl")

	want := Observation{Date: "2026-08-22", Source: "PR #99", Verdict: VerdictRenders, Notes: "real threads"}
	if err := AppendObservation(path, want); err != nil {
		t.Fatalf("AppendObservation: %v", err)
	}

	got, err := ReadObservations(path)
	if err != nil {
		t.Fatalf("ReadObservations: %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want [%+v]", got, want)
	}
}

func TestAppendObservationNeverRewritesAnEarlierLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dashboard.jsonl")

	first := Observation{Date: "2026-08-20", Source: "walk agent-tui#94", Verdict: VerdictEmpty, Notes: "stuck"}
	second := Observation{Date: "2026-08-22", Source: "PR #97", Verdict: VerdictRenders, Notes: "fixed"}
	if err := AppendObservation(path, first); err != nil {
		t.Fatal(err)
	}
	if err := AppendObservation(path, second); err != nil {
		t.Fatal(err)
	}

	got, err := ReadObservations(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d observations, want 2 -- append must never overwrite an earlier one", len(got))
	}
	if got[0] != first {
		t.Errorf("first observation = %+v, want %+v (append-only: the earlier entry must survive unchanged)", got[0], first)
	}
	if got[1] != second {
		t.Errorf("second observation = %+v, want %+v", got[1], second)
	}
}

func TestReadObservationsMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	obs, err := ReadObservations(filepath.Join(dir, "never-measured.jsonl"))
	if err != nil {
		t.Fatalf("error = %v, want nil -- a route nobody has measured yet is not a read failure", err)
	}
	if obs != nil {
		t.Fatalf("got %+v, want nil", obs)
	}
}

// TestLatestPicksTheNewestDateNotTheLastLine is the property that makes
// two lanes' near-simultaneous appends safe regardless of which order git
// happens to merge them in: whichever entry has the LATER Date always
// wins, even if it landed first in the file.
func TestLatestPicksTheNewestDateNotTheLastLine(t *testing.T) {
	obs := []Observation{
		{Date: "2026-08-22", Source: "newer, appended first", Verdict: VerdictRenders},
		{Date: "2026-08-20", Source: "older, appended second", Verdict: VerdictStub},
	}
	got, ok := Latest(obs)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.Date != "2026-08-22" {
		t.Fatalf("Latest picked %+v, want the 2026-08-22 entry regardless of file order", got)
	}
}

func TestLatestOfEmptyIsNotOK(t *testing.T) {
	_, ok := Latest(nil)
	if ok {
		t.Fatal("ok = true for an empty slice, want false (never measured is a distinct, typed state)")
	}
}

// TestTwoRoutesNeverConflict is agent-b3.md's own required demonstration:
// not an assertion that the new structure "should" avoid conflicts, but a
// REAL git merge of two branches that each append one observation to a
// DIFFERENT route's file -- the exact shape "lane A measures Dashboard,
// lane B measures Chat" takes under this scheme. Before this fix, both
// lanes edited the SAME testdata/vhs/full-nav-walk-report.md and every
// such pair of PRs conflicted (agent-b3.md's own brief names three:
// #97/#98/#99). Skips if git is not on PATH rather than failing a build
// environment that lacks it.
func TestTwoRoutesNeverConflict(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-q", "-b", "main")
	obsDir := filepath.Join(repo, "observations")
	if err := os.MkdirAll(obsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Base state: both routes already have one observation each, committed
	// to main -- the shared ancestor both lanes branch from.
	if err := AppendObservation(filepath.Join(obsDir, "dashboard.jsonl"),
		Observation{Date: "2026-08-15", Source: "walk agent-tui#94", Verdict: VerdictEmpty, Notes: "stuck"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendObservation(filepath.Join(obsDir, "chat.jsonl"),
		Observation{Date: "2026-08-15", Source: "walk agent-tui#94", Verdict: VerdictRenders, Notes: "fixture only"}); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "base")

	// Lane A: branches off main, measures ONLY Dashboard.
	run("checkout", "-q", "-b", "lane-a")
	if err := AppendObservation(filepath.Join(obsDir, "dashboard.jsonl"),
		Observation{Date: "2026-08-22", Source: "PR #97 (lane A)", Verdict: VerdictRenders, Notes: "fixed"}); err != nil {
		t.Fatal(err)
	}
	run("commit", "-aq", "-m", "lane A: dashboard fixed")

	// Lane B: branches off the SAME base, measures ONLY Chat -- a
	// DIFFERENT route, simulating the real #97/#99 scenario where two
	// lanes measured two different nav destinations.
	run("checkout", "-q", "main")
	run("checkout", "-q", "-b", "lane-b")
	if err := AppendObservation(filepath.Join(obsDir, "chat.jsonl"),
		Observation{Date: "2026-08-22", Source: "PR #99 (lane B)", Verdict: VerdictRenders, Notes: "real source"}); err != nil {
		t.Fatal(err)
	}
	run("commit", "-aq", "-m", "lane B: chat wired to a real source")

	// Merge lane A into main, then merge lane B into main -- the exact
	// sequence "#97 merges, then #99 merges" takes. If this were still one
	// shared table file both lanes edited, the second merge would conflict
	// (agent-b3.md's own brief: this happened for real, three times). Per
	// route storage means these are two different files; git's own merge
	// must resolve automatically with no conflict markers.
	run("checkout", "-q", "main")
	run("merge", "-q", "--no-edit", "lane-a")
	mergeCmd := exec.Command(gitBin, "merge", "--no-edit", "lane-b")
	mergeCmd.Dir = repo
	mergeCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := mergeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("merging lane-b into main (after lane-a) FAILED -- the exact collision this structure exists to prevent: %v\n%s", err, out)
	}

	// Confirm both lanes' own measurements actually survived the merge --
	// not just "no conflict markers," but the real content each lane
	// wrote is present and correct in the merged tree.
	dashObs, err := ReadObservations(filepath.Join(obsDir, "dashboard.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if latest, ok := Latest(dashObs); !ok || latest.Source != "PR #97 (lane A)" {
		t.Errorf("dashboard's latest observation = %+v, want lane A's -- merge must not have discarded it", latest)
	}
	chatObs, err := ReadObservations(filepath.Join(obsDir, "chat.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if latest, ok := Latest(chatObs); !ok || latest.Source != "PR #99 (lane B)" {
		t.Errorf("chat's latest observation = %+v, want lane B's -- merge must not have discarded it", latest)
	}
}
