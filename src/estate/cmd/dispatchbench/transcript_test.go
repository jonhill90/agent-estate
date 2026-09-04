package main

import (
	"fmt"
	"strings"
	"testing"
)

// A real two-turn shape, trimmed from a transcript this program actually
// produced during a smoke run on 2026-09-03: a prompt, a tool_use answer, the
// tool RESULT (also type "user", content an array), the end_turn answer, then
// a second prompt with a sidechain call inside it.
const fixture = `{"type":"user","isSidechain":false,"timestamp":"2026-09-04T01:00:29.184Z","cwd":"/tmp/x","message":{"role":"user","content":"Read f01.txt"}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-04T01:00:32.111Z","message":{"model":"claude-opus-5","stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":104,"cache_read_input_tokens":0,"cache_creation_input_tokens":36152}}}
{"type":"user","isSidechain":false,"timestamp":"2026-09-04T01:00:32.132Z","message":{"role":"user","content":[{"tool_use_id":"toolu_01","type":"tool_result","content":"file body"}]}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-04T01:00:34.111Z","message":{"model":"claude-opus-5","stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":36152,"cache_creation_input_tokens":130}}}
{"type":"user","isSidechain":false,"timestamp":"2026-09-04T01:00:40.000Z","message":{"role":"user","content":"Read f02.txt"}}
{"type":"user","isSidechain":true,"timestamp":"2026-09-04T01:00:41.000Z","message":{"role":"user","content":"sub-agent prompt"}}
{"type":"assistant","isSidechain":true,"timestamp":"2026-09-04T01:00:42.000Z","message":{"model":"claude-haiku-4-5-20251001","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":7,"cache_read_input_tokens":11,"cache_creation_input_tokens":13}}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-04T01:00:43.000Z","message":{"model":"claude-opus-5","stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}
`

// The discriminator that matters. A tool result is ALSO recorded as type
// "user"; counting those as prompts reports one turn as three, which would
// make every per-turn figure in the decision record wrong by the number of
// tool calls the turn happened to make.
func TestToolResultsAreNotCountedAsTurns(t *testing.T) {
	turns, err := parseTranscript(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("want 2 turns, got %d: %+v", len(turns), turns)
	}
	if turns[0].Prompt != "Read f01.txt" || turns[1].Prompt != "Read f02.txt" {
		t.Fatalf("turn prompts are wrong: %q, %q", turns[0].Prompt, turns[1].Prompt)
	}
}

// Every API call in a turn is part of what the turn cost, including a
// sub-agent's. A turn totalled from the final message alone would under-report
// the persistent arm by exactly the sub-agent work the estate is built to do.
func TestTurnTotalsCoverEveryCallIncludingSidechains(t *testing.T) {
	turns, _ := parseTranscript(strings.NewReader(fixture))
	in, out, cr, cc := turns[0].Totals()
	if in != 4 || out != 107 || cr != 36152 || cc != 36282 {
		t.Fatalf("turn 1 totals wrong: in=%d out=%d cacheRead=%d cacheCreate=%d", in, out, cr, cc)
	}
	in2, out2, cr2, cc2 := turns[1].Totals()
	if in2 != 6 || out2 != 9 || cr2 != 14 || cc2 != 17 {
		t.Fatalf("turn 2 totals dropped the sidechain: in=%d out=%d cacheRead=%d cacheCreate=%d", in2, out2, cr2, cc2)
	}
}

// The completion signal the poller waits on. Turn 1 finished; turn 2's last
// MAIN-conversation call is still tool_use, so it has not -- even though a
// sidechain inside it did end_turn. Reading the sidechain's ending as the
// turn's would send the next prompt into a busy pane.
func TestCompleteReadsTheMainConversationNotASidechain(t *testing.T) {
	turns, _ := parseTranscript(strings.NewReader(fixture))
	if !turns[0].Complete() {
		t.Fatal("turn 1 ended with stop_reason end_turn and was not reported complete")
	}
	if turns[1].Complete() {
		t.Fatal("turn 2 was reported complete on the strength of a sub-agent's end_turn; the main conversation is still in tool_use")
	}
}

// A prompt with no answer yet is the state the poller spends most of its time
// in, and it must not read as finished.
func TestAnUnansweredPromptIsNotComplete(t *testing.T) {
	turns, err := parseTranscript(strings.NewReader(`{"type":"user","message":{"content":"hello"}}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Complete() {
		t.Fatalf("an unanswered prompt was reported complete: %+v", turns)
	}
}

// The file is appended to by a live process while this reads it, so the last
// line is regularly half-written. A parse that failed there would make the
// poller time out on turns that had actually finished.
func TestATruncatedFinalLineIsSkippedNotFatal(t *testing.T) {
	turns, err := parseTranscript(strings.NewReader(fixture + `{"type":"assistant","mess`))
	if err != nil {
		t.Fatalf("a truncated final line was fatal: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(turns))
	}
}

// THE INSTRUMENT CHECK for every transcript-derived figure in this program.
//
// These records are the real ones Claude Code wrote for a single stateless
// turn on 2026-09-03, trimmed to the fields this file reads. The same turn's
// `claude -p --output-format json` envelope reported, first-party:
//
//	input 6, cache read 80497, cache creation 20519, output 365
//
// Note the pairs sharing a message.id: one API response, two content blocks,
// two lines, the SAME usage repeated. Summed naively they give 127667 cache
// reads against the envelope's 80497. If this test passes, the persistent
// arm's tokens -- which have no envelope to check them against -- are being
// read the same way the stateless arm's checkable ones are.
const realTurnFixture = `{"type":"user","isSidechain":false,"message":{"role":"user","content":"Read the file f01.txt in this directory."}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-04T01:15:18.309Z","message":{"id":"msg_011CehWnNwHn3RdeParFxSVv","model":"claude-opus-5","stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":115,"cache_read_input_tokens":15565,"cache_creation_input_tokens":16040}}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-04T01:15:20.024Z","message":{"id":"msg_011CehWnNwHn3RdeParFxSVv","model":"claude-opus-5","stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":115,"cache_read_input_tokens":15565,"cache_creation_input_tokens":16040}}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-04T01:15:22.194Z","message":{"id":"msg_011CehWnd75oawe442aR3QjQ","model":"claude-opus-5","stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":243,"cache_read_input_tokens":31605,"cache_creation_input_tokens":1722}}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-04T01:15:24.733Z","message":{"id":"msg_011CehWnd75oawe442aR3QjQ","model":"claude-opus-5","stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":243,"cache_read_input_tokens":31605,"cache_creation_input_tokens":1722}}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-04T01:15:26.196Z","message":{"id":"msg_011CehWo1m6oMjMTCrq42nAm","model":"claude-opus-5","stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":7,"cache_read_input_tokens":33327,"cache_creation_input_tokens":2757}}}
`

func TestTranscriptTotalsMatchTheEnvelopeForTheSameTurn(t *testing.T) {
	turns, err := parseTranscript(strings.NewReader(realTurnFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("want 1 turn, got %d", len(turns))
	}
	in, out, cr, cc := turns[0].Totals()
	// The envelope `claude -p` printed for this exact turn.
	const wantIn, wantOut, wantCR, wantCC = 6, 365, 80497, 20519
	if in != wantIn || out != wantOut || cr != wantCR || cc != wantCC {
		t.Fatalf("transcript disagrees with the harness's own envelope for the same turn:\n got in=%d out=%d cacheRead=%d cacheCreate=%d\nwant in=%d out=%d cacheRead=%d cacheCreate=%d",
			in, out, cr, cc, wantIn, wantOut, wantCR, wantCC)
	}
}

// The mutation direction: without deduplication these SAME records total
// 127667 cache reads. If this ever stops holding, the fixture no longer
// contains repeated responses and the test above has quietly stopped
// testing anything.
func TestTheFixtureReallyDoesContainRepeatedResponses(t *testing.T) {
	var naive int64
	for _, line := range strings.Split(realTurnFixture, "\n") {
		if !strings.Contains(line, `"type":"assistant"`) {
			continue
		}
		i := strings.Index(line, `"cache_read_input_tokens":`)
		var v int64
		if _, err := fmt.Sscanf(line[i+len(`"cache_read_input_tokens":`):], "%d", &v); err != nil {
			t.Fatal(err)
		}
		naive += v
	}
	if naive != 127667 {
		t.Fatalf("the naive sum of the fixture is %d, not the 127667 that was actually measured; the fixture has changed", naive)
	}
}
