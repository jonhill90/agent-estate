package chat

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonhill90/agent-tui/internal/theme"
)

// TestParseMentionsDedupesCaseInsensitivelyInFirstSeenOrder proves the one
// parse both ValidateMentions and highlightMentions share behaves the way
// both depend on: distinct tokens, first-seen order, "@Bob" and "@bob"
// treated as the same mention. "hi@bob" (no word boundary before "@") is
// included deliberately -- mentionPattern's own doc comment: a mention
// glued to other text is worth flagging, not silently ignored.
func TestParseMentionsDedupesCaseInsensitivelyInFirstSeenOrder(t *testing.T) {
	got := ParseMentions("cc @bob and @alice, also @Bob again, and hi@carol too")
	want := []string{"bob", "alice", "carol"}
	if len(got) != len(want) {
		t.Fatalf("ParseMentions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseMentions = %v, want %v", got, want)
		}
	}
}

// TestParseMentionsNoneReturnsNil confirms plain text with no "@" at all
// parses to nothing to validate or highlight -- the common case.
func TestParseMentionsNoneReturnsNil(t *testing.T) {
	if got := ParseMentions("just an ordinary message"); got != nil {
		t.Fatalf("ParseMentions(no mentions) = %v, want nil", got)
	}
}

// TestValidateMentionsResolvesKnownRunningParticipant is the mutation
// check's "real, running participant" direction: a message addressing a
// participant that IS in the room and IS running must validate clean.
func TestValidateMentionsResolvesKnownRunningParticipant(t *testing.T) {
	participants := []Participant{{Name: "bob", Session: "s", Running: true}}
	if err := ValidateMentions("hey @bob, status?", participants); err != nil {
		t.Fatalf("ValidateMentions of a known, running participant = %v, want nil", err)
	}
}

// TestValidateMentionsRefusesUnknownParticipant is the mutation check's
// other direction: agent-tui#114's own failure mode ("must say so at
// compose time"), for a name the room has never heard of.
func TestValidateMentionsRefusesUnknownParticipant(t *testing.T) {
	err := ValidateMentions("hey @ghost, status?", []Participant{{Name: "bob", Running: true}})
	if err == nil {
		t.Fatal("ValidateMentions of an unknown participant = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "@ghost") || !strings.Contains(err.Error(), "not in this room") {
		t.Fatalf("ValidateMentions error = %q, want it to name @ghost as not in this room", err.Error())
	}
}

// TestValidateMentionsRefusesStoppedParticipant is the second half of the
// same failure mode: a name the room DOES know, but that is not running
// right now -- a distinct reason from "unknown", and must say so.
func TestValidateMentionsRefusesStoppedParticipant(t *testing.T) {
	err := ValidateMentions("hey @bob, status?", []Participant{{Name: "bob", Running: false}})
	if err == nil {
		t.Fatal("ValidateMentions of a stopped participant = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "@bob") || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("ValidateMentions error = %q, want it to name @bob as not running", err.Error())
	}
}

// TestValidateMentionsNoMentionsAlwaysPasses proves an ordinary message
// with no "@" at all is never blocked by an empty or unrelated roster --
// mention validation must not become a gate on plain text.
func TestValidateMentionsNoMentionsAlwaysPasses(t *testing.T) {
	if err := ValidateMentions("no mentions here", nil); err != nil {
		t.Fatalf("ValidateMentions(no mentions, no roster) = %v, want nil", err)
	}
}

// TestHighlightMentionsStylesTheTokenDistinctFromPlainText proves the
// rendering half of agent-tui#114: "mentions must be visible in the rendered
// transcript as mentions, not as plain text that happens to start with @."
func TestHighlightMentionsStylesTheTokenDistinctFromPlainText(t *testing.T) {
	// go test's own stdout is not a terminal, so lipgloss's default
	// renderer auto-detects termenv.Ascii (no escape codes at all) and
	// every Style.Render call becomes a silent no-op -- forcing a real
	// colour profile here is what makes this test able to fail at all
	// (verify-the-instrument: prove the check can actually fail before
	// trusting it passes). Restored after, so this does not leak into any
	// other test in the package.
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	st := (Model{theme: theme.Default}).styles()
	plain := "you: hello @bob, are you there"
	got := highlightMentions([]string{plain}, st)[0]
	if got == plain {
		t.Fatal("highlightMentions left the line byte-for-byte identical -- the mention was not styled")
	}
	want := st.mention.Render("@bob")
	if !strings.Contains(got, want) {
		t.Fatalf("highlightMentions output = %q, want it to contain the styled token %q", got, want)
	}
}

// TestTrySendRefusesMentionOfAbsentParticipantWithoutCallingSender is the
// model-level compose-time gate, mutation-checked in the failure
// direction: agent-tui#114's own scenario ("an @-mention that names a
// participant who is not in the room... must say so at compose time") AND
// the transport must never be reached for a refused send -- the composer
// staying editable with a visible reason is not enough if the daemon was
// still called underneath it.
func TestTrySendRefusesMentionOfAbsentParticipantWithoutCallingSender(t *testing.T) {
	senderCalled := false
	m := fetched(t, 100, 30).
		WithSender(func(threadID, text string) error { senderCalled = true; return nil })
	next, _ := m.Update(participantsFetchMsg{participants: []Participant{{Name: "alice", Running: true}}})
	m = next.(Model)
	m = sendKey(t, m, "2")
	m = sendKey(t, m, "i")
	m.composer.SetValue("@ghost please look at this")

	next, cmd := m.handleComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)

	if cmd != nil {
		t.Fatal("trySend returned a Cmd for a refused mention -- the daemon transport must never be reached")
	}
	if senderCalled {
		t.Fatal("Sender was called despite an unresolved @-mention -- the exact failure agent-tui#114 exists to prevent")
	}
	if m.sendOutcome != sendFailed {
		t.Fatalf("sendOutcome = %v, want sendFailed", m.sendOutcome)
	}
	if !strings.Contains(m.View(), "@ghost") || !strings.Contains(m.View(), "not in this room") {
		t.Fatalf("refusal reason not rendered:\n%s", m.View())
	}
}

// TestTrySendSendsWhenMentionResolvesToARunningParticipant is the other
// mutation-check direction: a real, running participant must not be
// refused -- the send proceeds to the (fake) Sender exactly as an
// unmentioned message would.
func TestTrySendSendsWhenMentionResolvesToARunningParticipant(t *testing.T) {
	var gotText string
	m := fetched(t, 100, 30).
		WithSender(func(threadID, text string) error { gotText = text; return nil })
	next, _ := m.Update(participantsFetchMsg{participants: []Participant{{Name: "alice", Running: true}}})
	m = next.(Model)
	m = sendKey(t, m, "2")
	m = sendKey(t, m, "i")

	_, resolved := runSend(t, m, "@alice can you take this")

	if gotText != "@alice can you take this" {
		t.Fatalf("Sender received %q, want the composed text unmodified", gotText)
	}
	if resolved.sendOutcome != sendIdle {
		t.Fatalf("sendOutcome after a resolvable mention = %v, want sendIdle (delivered)", resolved.sendOutcome)
	}
}

// TestUnknownSendOutcomeSurvivesAValidMention is the regression agent-tui#114's own
// verification list asks for by name: "a test that an unknown-fate send
// still renders as unknown after the composer change -- do not let the new
// path collapse the three states into two." A message with a mention that
// DOES resolve must still reach the Sender and still land on sendUnknown,
// not be reinterpreted as failed or delivered by anything mention
// validation added.
func TestUnknownSendOutcomeSurvivesAValidMention(t *testing.T) {
	m := fetched(t, 100, 30).
		WithSender(func(threadID, text string) error {
			return fmt.Errorf("%w: mcp: session_send: no reply within 20m0s", ErrUnknown)
		})
	next, _ := m.Update(participantsFetchMsg{participants: []Participant{{Name: "alice", Running: true}}})
	m = next.(Model)
	m = sendKey(t, m, "2")
	m = sendKey(t, m, "i")

	_, resolved := runSend(t, m, "@alice ping")

	if resolved.sendOutcome != sendUnknown {
		t.Fatalf("sendOutcome = %v, want sendUnknown -- mention validation must not collapse this to failed", resolved.sendOutcome)
	}
	if !strings.Contains(resolved.View(), "unknown") {
		t.Fatalf("unknown state not rendered:\n%s", resolved.View())
	}
}
