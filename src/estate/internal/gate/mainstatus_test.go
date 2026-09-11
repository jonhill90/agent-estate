package gate

import (
	"errors"
	"strings"
	"testing"
)

func TestMainGreenSuccessPasses(t *testing.T) {
	green, reason := MainGreen(MainRun{ID: 1, Conclusion: "success", HeadSHA: "abc123"}, true)
	if !green {
		t.Fatalf("success run should be green, got refused: %s", reason)
	}
	if reason != "" {
		t.Fatalf("a green result should carry no reason, got %q", reason)
	}
}

func TestMainGreenFailureRefuses(t *testing.T) {
	green, reason := MainGreen(MainRun{ID: 2, Conclusion: "failure", HeadSHA: "deadbeef"}, true)
	if green {
		t.Fatal("a failed run must not be reported green")
	}
	if reason == "" {
		t.Fatal("a refusal must carry a reason")
	}
}

func TestMainGreenPendingIsNotAPass(t *testing.T) {
	// Mirrors gate.go's own checksGreen: a still-running check is not a
	// completed success, and must not be treated as one.
	green, _ := MainGreen(MainRun{ID: 3, Conclusion: ""}, true)
	if green {
		t.Fatal("an in-progress/queued run must not be reported green")
	}
}

func TestMainGreenNoRunOnRecordRefuses(t *testing.T) {
	// "cannot tell" is never "allowed" anywhere else in this package
	// (checksGreen's own "no checks reported" case); this must match.
	green, reason := MainGreen(MainRun{}, false)
	if green {
		t.Fatal("no run on record must not default to green")
	}
	if reason == "" {
		t.Fatal("must say why it refused")
	}
}

func TestMainStatusReasonGreenRunPermitsSilently(t *testing.T) {
	orig := listMainRuns
	defer func() { listMainRuns = orig }()
	listMainRuns = func(repo, branch, workflow string) (MainRun, bool, error) {
		return MainRun{ID: 10, Conclusion: "success", HeadSHA: "cafef00d"}, true, nil
	}
	note, refuse := MainStatusReason("o/r", "main", "estate-ci", "")
	if refuse {
		t.Fatal("a green run must not refuse")
	}
	if note != "" {
		t.Fatalf("a green run must not add a note, got %q", note)
	}
}

// TestBypass_RedMainRefusesWithoutOverride is the mutation-check the brief
// asks for on the refusing side: make main appear red, confirm it refuses.
func TestBypass_RedMainRefusesWithoutOverride(t *testing.T) {
	orig := listMainRuns
	defer func() { listMainRuns = orig }()
	listMainRuns = func(repo, branch, workflow string) (MainRun, bool, error) {
		return MainRun{ID: 11, Conclusion: "failure", HeadSHA: "badc0de1"}, true, nil
	}
	note, refuse := MainStatusReason("o/r", "main", "estate-ci", "")
	if !refuse {
		t.Fatal("a red main run must refuse")
	}
	if note == "" {
		t.Fatal("a refusal must carry a reason")
	}
}

// TestBypass_RedMainOverrideActuallyWorks proves the escape valve the brief
// requires proof of, not just an assertion it exists: an explicit,
// non-empty override on a red run must flip refuse to false, and the note
// must still say the branch was red and that an override was used -- never
// silent.
func TestBypass_RedMainOverrideActuallyWorks(t *testing.T) {
	orig := listMainRuns
	defer func() { listMainRuns = orig }()
	listMainRuns = func(repo, branch, workflow string) (MainRun, bool, error) {
		return MainRun{ID: 12, Conclusion: "failure", HeadSHA: "badc0de2"}, true, nil
	}
	note, refuse := MainStatusReason("o/r", "main", "estate-ci", "known flaky, tracked in #9999")
	if refuse {
		t.Fatal("an explicit override must permit the merge")
	}
	if note == "" {
		t.Fatal("an override must still be logged, never silent")
	}
	if !strings.Contains(note, "override") {
		t.Fatalf("override note must say it was an override, got %q", note)
	}
	if !strings.Contains(note, "red") {
		t.Fatalf("override note must still say main was red, got %q", note)
	}
}

func TestMainStatusReasonFetchErrorRefuses(t *testing.T) {
	orig := listMainRuns
	defer func() { listMainRuns = orig }()
	listMainRuns = func(repo, branch, workflow string) (MainRun, bool, error) {
		return MainRun{}, false, errors.New("network unreachable")
	}
	note, refuse := MainStatusReason("o/r", "main", "estate-ci", "")
	if !refuse {
		t.Fatal("an unreadable CI state must refuse, not assume green")
	}
	if note == "" {
		t.Fatal("must say why")
	}
}
