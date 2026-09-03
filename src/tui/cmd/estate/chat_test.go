package main

import (
	"errors"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/src/tui/internal/chat"
	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
	"github.com/jonhill90/agent-estate/src/tui/internal/lane"
)

// TestBuildParticipantsFetch_DerivesRunningFromLaneState proves the join
// this file performs: every lane across every session becomes one
// chat.Participant, Running false only for "dead"/"stale" -- the exact
// same rule internal/agents/row.go's modeFor already uses, so this pane
// can never show a lane as gone in the Agents pane while still validating
// an @-mention against it here as running.
func TestBuildParticipantsFetch_DerivesRunningFromLaneState(t *testing.T) {
	fetch := buildParticipantsFetch(func() ([]lane.Session, error) {
		return []lane.Session{
			{Name: "s1", Lanes: []lane.Lane{
				{Name: "alice", State: "busy"},
				{Name: "bob", State: "dead"},
				{Name: "carol", State: "stale"},
			}},
		}, nil
	}, nil)

	got, err := fetch()
	if err != nil {
		t.Fatalf("fetch() = %v, want nil", err)
	}
	want := map[string]bool{"alice": true, "bob": false, "carol": false}
	if len(got) != len(want) {
		t.Fatalf("fetch() = %+v, want %d participants", got, len(want))
	}
	for _, p := range got {
		if p.Running != want[p.Name] {
			t.Errorf("participant %q Running = %v, want %v", p.Name, p.Running, want[p.Name])
		}
	}
}

// TestBuildParticipantsFetch_SkipsUnreadableSessions mirrors
// internal/agents.Derive's own rule (row.go: "a session with a non-empty
// Error still contributes no rows for its own lanes... unreadable, not
// 'no lanes'") -- a session sessions.sh could not read must not silently
// donate a stale, no-longer-true participant list to the room's roster.
func TestBuildParticipantsFetch_SkipsUnreadableSessions(t *testing.T) {
	fetch := buildParticipantsFetch(func() ([]lane.Session, error) {
		return []lane.Session{
			{Name: "broken", Error: "session closed mid-read", Lanes: []lane.Lane{{Name: "ghost", State: "busy"}}},
			{Name: "ok", Lanes: []lane.Lane{{Name: "alice", State: "busy"}}},
		}, nil
	}, nil)

	got, err := fetch()
	if err != nil {
		t.Fatalf("fetch() = %v, want nil", err)
	}
	if len(got) != 1 || got[0].Name != "alice" {
		t.Fatalf("fetch() = %+v, want only alice (the unreadable session's lanes must not appear)", got)
	}
}

// TestBuildParticipantsFetch_PropagatesFetchError confirms a real
// sessionsFetch failure (e.g. no supervisor connection) surfaces as an
// error rather than an empty, falsely-authoritative roster -- the same
// "blind, not quiet" rule this module applies to every other Fetcher.
func TestBuildParticipantsFetch_PropagatesFetchError(t *testing.T) {
	wantErr := errors.New("no supervisor connection")
	fetch := buildParticipantsFetch(func() ([]lane.Session, error) { return nil, wantErr }, nil)

	_, err := fetch()
	if !errors.Is(err, wantErr) {
		t.Fatalf("fetch() err = %v, want %v", err, wantErr)
	}
}

// TestBuildParticipantsFetch_BothNilIsANilFetcher proves
// chatModel.WithParticipants(nil) degrades exactly like WithSender(nil) --
// never wrapping two nil dependencies in a closure that would panic the
// first time it is called.
func TestBuildParticipantsFetch_BothNilIsANilFetcher(t *testing.T) {
	var fetch chat.ParticipantsFetcher = buildParticipantsFetch(nil, nil)
	if fetch != nil {
		t.Fatal("buildParticipantsFetch(nil, nil) returned a non-nil Fetcher")
	}
}

// TestBuildParticipantsFetch_LedgerFallbackFillsInWhenSessionsFetchIsNil is
// agent-estate#930's own reproduction: sessionsFetch reads the deleted
// Python MCP server and is nil/always-erroring in a real launch. The ledger
// fallback must still produce a real, addressable roster from in-flight
// dispatches.
func TestBuildParticipantsFetch_LedgerFallbackFillsInWhenSessionsFetchIsNil(t *testing.T) {
	fetch := buildParticipantsFetch(nil, func() estatus.Status {
		return estatus.Status{
			InFlight: []estatus.Dispatch{
				{ID: "930-1", Issue: "#930", State: "dispatched", At: time.Now()},
			},
		}
	})

	got, err := fetch()
	if err != nil {
		t.Fatalf("fetch() = %v, want nil", err)
	}
	if len(got) != 1 || got[0].Name != "930-1" || !got[0].Running {
		t.Fatalf("fetch() = %+v, want one running participant \"930-1\"", got)
	}
}

// TestBuildParticipantsFetch_LedgerFallbackSuppressesSessionsErrorWhenItHasData
// confirms a dead MCP server's error does not blank a roster the ledger
// fallback could still populate -- ValidateMentions needs a real list to
// check against, not a total failure just because one of two sources is
// down.
func TestBuildParticipantsFetch_LedgerFallbackSuppressesSessionsErrorWhenItHasData(t *testing.T) {
	fetch := buildParticipantsFetch(
		func() ([]lane.Session, error) { return nil, errors.New("no supervisor connection") },
		func() estatus.Status {
			return estatus.Status{InFlight: []estatus.Dispatch{{ID: "930-1", State: "dispatched", At: time.Now()}}}
		},
	)

	got, err := fetch()
	if err != nil {
		t.Fatalf("fetch() = %v, want nil (ledger fallback found participants)", err)
	}
	if len(got) != 1 || got[0].Name != "930-1" {
		t.Fatalf("fetch() = %+v, want one participant \"930-1\"", got)
	}
}
