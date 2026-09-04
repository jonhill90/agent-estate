package library

import (
	"errors"
	"strings"
	"testing"
)

func fakeRunner(t *testing.T, wantContains []string, out string, err error) func(args []string) ([]byte, error) {
	return func(args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		for _, want := range wantContains {
			if !strings.Contains(joined, want) {
				t.Fatalf("args = %v, want it to contain %q", args, want)
			}
		}
		return []byte(out), err
	}
}

func TestReadItemsSetsQueryOnlyAndView(t *testing.T) {
	run := fakeRunner(t, []string{"-json", "/tmp/copy.sqlite3", "PRAGMA query_only=1", "FROM live_parameters"}, `[]`, nil)
	rows, err := ReadItems(run, "/tmp/copy.sqlite3", ViewLiveParameters, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

// TestReadItemsAcceptsNeedsReviewView is agent-estate#1089's own view-list
// contract at the query-builder level: ReadItems must accept needs_review
// as a valid view and query it by name -- fails against the parent commit
// (329b4ee), where ViewNeedsReview did not exist and this view was refused
// as "unknown".
func TestReadItemsAcceptsNeedsReviewView(t *testing.T) {
	run := fakeRunner(t, []string{"-json", "/tmp/copy.sqlite3", "PRAGMA query_only=1", "FROM needs_review"}, `[]`, nil)
	if _, err := ReadItems(run, "/tmp/copy.sqlite3", ViewNeedsReview, "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestReadItemsDecodesRealShape(t *testing.T) {
	out := `[{"id":"it-abf738372b578388","kind":"parameter","weight":"hard","status":"acknowledged","resolved_to":"scheduler=claude_code_native_cron_only","body_snippet":"Never set up a cron outside the Claude Code ecosystem"}]`
	run := fakeRunner(t, []string{"live_parameters"}, out, nil)
	rows, err := ReadItems(run, "x.sqlite3", ViewLiveParameters, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "it-abf738372b578388" || rows[0].ResolvedTo != "scheduler=claude_code_native_cron_only" {
		t.Fatalf("got %+v", rows)
	}
}

func TestReadItemsAppliesWeightAndStatusFilters(t *testing.T) {
	run := fakeRunner(t, []string{"i.weight = 'hard'", "i.status = 'open'"}, `[]`, nil)
	if _, err := ReadItems(run, "x.sqlite3", ViewUnacknowledged, "hard", "open"); err != nil {
		t.Fatal(err)
	}
}

func TestReadItemsRejectsUnknownView(t *testing.T) {
	if _, err := ReadItems(nil, "x.sqlite3", View("drop table items"), "", ""); err == nil {
		t.Fatal("expected an error for an unknown view, got nil")
	}
}

func TestReadItemsRejectsUnknownWeightFilter(t *testing.T) {
	if _, err := ReadItems(nil, "x.sqlite3", ViewLiveParameters, "'; DROP TABLE items; --", ""); err == nil {
		t.Fatal("expected an error for an unknown weight filter, got nil")
	}
}

func TestReadItemsRejectsUnknownStatusFilter(t *testing.T) {
	if _, err := ReadItems(nil, "x.sqlite3", ViewLiveParameters, "", "not-a-real-status"); err == nil {
		t.Fatal("expected an error for an unknown status filter, got nil")
	}
}

func TestReadItemsPropagatesRunnerError(t *testing.T) {
	run := fakeRunner(t, nil, "", errors.New("boom"))
	if _, err := ReadItems(run, "x.sqlite3", ViewLiveParameters, "", ""); err == nil {
		t.Fatal("expected an error")
	}
}

func TestReadItemDetailDecodesRealShape(t *testing.T) {
	out := `[{"id":"it-abf738372b578388","kind":"parameter","weight":"hard","status":"acknowledged","status_reason":"","resolved_to":"scheduler=claude_code_native_cron_only","body":"Never set up a cron outside the Claude Code ecosystem -- crons must be Claude Code-native.","prompt_id":"mp-18bd274069bdadc6","prompt_at":1787424786,"prompt_context":"context text","prompt_text":"yea. you are going away"}]`
	run := fakeRunner(t, []string{"JOIN prompts"}, out, nil)
	d, err := ReadItemDetail(run, "x.sqlite3", "it-abf738372b578388")
	if err != nil {
		t.Fatal(err)
	}
	if d.Body != "Never set up a cron outside the Claude Code ecosystem -- crons must be Claude Code-native." {
		t.Errorf("Body = %q", d.Body)
	}
	if d.PromptID != "mp-18bd274069bdadc6" || d.PromptContext != "context text" {
		t.Errorf("got %+v", d)
	}
}

func TestReadItemDetailRejectsMalformedID(t *testing.T) {
	if _, err := ReadItemDetail(nil, "x.sqlite3", "it-abf7'; DROP TABLE items; --"); err == nil {
		t.Fatal("expected an error for a malformed id, got nil")
	}
}

func TestReadItemDetailNoRowsIsAnError(t *testing.T) {
	run := fakeRunner(t, nil, `[]`, nil)
	if _, err := ReadItemDetail(run, "x.sqlite3", "it-0000000000000000"); err == nil {
		t.Fatal("expected an error for a missing item, got nil")
	}
}

func TestReadPossibilityCountDecodes(t *testing.T) {
	run := fakeRunner(t, []string{"possibility_count"}, `[{"count":931}]`, nil)
	n, err := ReadPossibilityCount(run, "x.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if n != 931 {
		t.Errorf("got %d, want 931", n)
	}
}

func TestReadPossibilityCountEmptyIsZeroNotError(t *testing.T) {
	run := fakeRunner(t, nil, `[]`, nil)
	n, err := ReadPossibilityCount(run, "x.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("got %d, want 0", n)
	}
}
