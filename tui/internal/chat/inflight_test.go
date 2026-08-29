package chat

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// runChatCmd executes cmd and every sub-command of any tea.BatchMsg it
// returns -- same helper internal/rail/inflight_test.go's own runCmd and
// internal/monitor/inflight_test.go's own runCmd provide, repeated here
// rather than imported (see those files' own doc comments for why a shared
// test helper isn't worth a new cross-package dependency).
func runChatCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, runChatCmd(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// TestWithParticipantsSeedsInFlightTrue mirrors internal/agents/model_test.go's
// New seed check: Init() always fires the first participants fetch
// unconditionally once WithParticipants is called, so the guard must
// already reflect that before the first participantsTickMsg
// (participantsRefreshInterval later) can check it.
func TestWithParticipantsSeedsInFlightTrue(t *testing.T) {
	m := New(NewFixtureSource()).WithParticipants(func() ([]Participant, error) { return nil, nil })
	if !m.participantsFetchInFlight {
		t.Fatal("WithParticipants must seed participantsFetchInFlight true -- Init() always fires the first fetch")
	}
}

// TestNoParticipantsFetcherDoesNotSeedInFlight confirms a Model with no
// ParticipantsFetcher wired (New's default) never reports a participants
// fetch outstanding.
func TestNoParticipantsFetcherDoesNotSeedInFlight(t *testing.T) {
	m := New(NewFixtureSource())
	if m.participantsFetchInFlight {
		t.Fatal("a Model with no ParticipantsFetcher must not seed participantsFetchInFlight true")
	}
}

// TestParticipantsTickMsgDoesNotOverlapInFlightFetch is agent-tui#177's
// core mutation check for the room roster: a participantsTickMsg landing
// while the previous fetch has not yet answered must NOT queue a second
// participants call against mcp_server.py's single-threaded stdio loop.
// Without the participantsFetchInFlight guard in the participantsTickMsg
// case, this test goes red (calls == 2).
func TestParticipantsTickMsgDoesNotOverlapInFlightFetch(t *testing.T) {
	calls := 0
	fetch := func() ([]Participant, error) {
		calls++
		return nil, errors.New("slow, not yet answered")
	}
	m := New(NewFixtureSource()).WithParticipants(fetch) // seeds participantsFetchInFlight true

	next, cmd := m.Update(participantsTickMsg{})
	m = next.(Model)
	for range runChatCmd(cmd) {
		// draining is enough -- calls is what this test asserts on
	}
	if calls != 0 {
		t.Fatalf("participantsTickMsg while a fetch is already in flight called fetch %d times, want 0 -- the in-flight guard should have blocked it", calls)
	}
}

// TestParticipantsFetchMsgClearsInFlightOnSuccess and
// TestParticipantsFetchMsgClearsInFlightOnError together prove the guard
// is cleared on every exit path, both directions of the brief's mutation
// check: a guard that leaks true after either outcome stops fetching the
// roster forever, so ValidateMentions keeps judging @-mentions against a
// permanently stale participant list.
func TestParticipantsFetchMsgClearsInFlightOnSuccess(t *testing.T) {
	m := New(NewFixtureSource()).WithParticipants(func() ([]Participant, error) { return nil, nil })
	if !m.participantsFetchInFlight {
		t.Fatal("WithParticipants must seed participantsFetchInFlight true")
	}
	next, _ := m.Update(participantsFetchMsg{participants: nil, err: nil})
	m = next.(Model)
	if m.participantsFetchInFlight {
		t.Fatal("a successful participantsFetchMsg must clear participantsFetchInFlight")
	}
}

func TestParticipantsFetchMsgClearsInFlightOnError(t *testing.T) {
	m := New(NewFixtureSource()).WithParticipants(func() ([]Participant, error) { return nil, nil })
	if !m.participantsFetchInFlight {
		t.Fatal("WithParticipants must seed participantsFetchInFlight true")
	}
	next, _ := m.Update(participantsFetchMsg{err: errors.New("no reply within 10s")})
	m = next.(Model)
	if m.participantsFetchInFlight {
		t.Fatal("a failed participantsFetchMsg must also clear participantsFetchInFlight -- a caller that leaks true on error stops fetching the roster forever")
	}
}
