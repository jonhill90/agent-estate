package main

import (
	"errors"
	"testing"

	"github.com/jonhill90/agent-estate/src/tui/internal/chat"
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
	})

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
	})

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
	fetch := buildParticipantsFetch(func() ([]lane.Session, error) { return nil, wantErr })

	_, err := fetch()
	if !errors.Is(err, wantErr) {
		t.Fatalf("fetch() err = %v, want %v", err, wantErr)
	}
}

// TestBuildParticipantsFetch_NilSessionsFetchIsANilFetcher proves
// chatModel.WithParticipants(nil) degrades exactly like WithSender(nil) --
// never wrapping a nil dependency in a closure that would panic the first
// time it is called.
func TestBuildParticipantsFetch_NilSessionsFetchIsANilFetcher(t *testing.T) {
	var fetch chat.ParticipantsFetcher = buildParticipantsFetch(nil)
	if fetch != nil {
		t.Fatal("buildParticipantsFetch(nil) returned a non-nil Fetcher")
	}
}
