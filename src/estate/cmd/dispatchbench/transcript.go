package main

// Reading a PERSISTENT lane's token usage.
//
// The stateless arm is easy: `claude -p --output-format json` prints one
// envelope carrying usage and a dollar figure, and internal/harness already
// parses it -- this benchmark uses that same parser rather than a second one,
// so the stateless arm is measured through the estate's own production path.
//
// An interactive `claude` in a tmux pane prints no such envelope. Its
// first-party record of the same numbers is the session transcript Claude
// Code writes itself, one JSON object per line, under
// ~/.claude/projects/<mangled cwd>/<session>.jsonl. Each assistant record
// carries the same four token counts the -p envelope reports, per API call.
//
// It carries NO dollar figure. That absence is reported as absence: this file
// will not multiply tokens by a price table, for the reason
// internal/harness's Spend doc comment gives -- estimating is the failure a
// spend ledger must not reintroduce. See docs/decisions/0001 for what is
// therefore compared between the arms and what is not.
//
// SCREEN SCRAPING WAS REJECTED as the read source here, for the same reason
// src/tui's chat pane rejected it (docs/tui/SPEC-shell.md S7): the pane shows
// a rendering, the transcript is the record.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// usage is one API call's token counts, as the harness itself reported them.
type usage struct {
	Model         string
	StopReason    string
	Input         int64
	Output        int64
	CacheRead     int64
	CacheCreation int64
	At            time.Time
	Sidechain     bool
}

// transcriptTurn is one operator prompt and every API call the harness made
// answering it -- including sub-agent (sidechain) calls, which are part of
// what the turn cost even though they are a different conversation.
type transcriptTurn struct {
	Prompt string
	Calls  []usage
}

// Complete reports whether the harness finished this turn. `end_turn` is the
// harness saying it stopped because it was done, as opposed to `tool_use`,
// where more calls follow. An empty turn is NOT complete -- a prompt recorded
// with no answer yet is exactly the state the poller is waiting out.
func (t transcriptTurn) Complete() bool {
	for i := len(t.Calls) - 1; i >= 0; i-- {
		if t.Calls[i].Sidechain {
			continue // a sub-agent ending is not the turn ending
		}
		return t.Calls[i].StopReason == "end_turn"
	}
	return false
}

func (t transcriptTurn) Totals() (in, out, cacheRead, cacheCreation int64) {
	for _, c := range t.Calls {
		in += c.Input
		out += c.Output
		cacheRead += c.CacheRead
		cacheCreation += c.CacheCreation
	}
	return
}

// Elapsed is the span from the turn's first API call to its last, which is
// NOT the same as the wall-clock the harness measures around send-and-wait:
// it excludes the time the prompt sat in the input box and whatever the
// renderer did afterwards. Both are reported; neither is presented as the
// other.
func (t transcriptTurn) Elapsed() time.Duration {
	if len(t.Calls) == 0 {
		return 0
	}
	return t.Calls[len(t.Calls)-1].At.Sub(t.Calls[0].At)
}

type transcriptRecord struct {
	Type        string          `json:"type"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Timestamp   string          `json:"timestamp"`
	Message     json.RawMessage `json:"message"`
}

type transcriptMessage struct {
	// ID is the API's own id for one response ("msg_..."), and it is the key
	// this file deduplicates on. See parseTranscript.
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	StopReason string          `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
	Usage      *struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// parseTranscript splits a session transcript into turns.
//
// A TURN STARTS at a top-level user record whose message content is a plain
// string. That shape is the discriminator that matters: a tool RESULT is also
// recorded as type "user", but its content is an array of tool_result blocks,
// and counting those as prompts would report one turn as five. Sidechain and
// meta records never start a turn -- a sub-agent's own prompt is part of the
// turn that spawned it.
//
// ONE API RESPONSE IS WRITTEN AS SEVERAL RECORDS, and each of them repeats
// the WHOLE response's usage. Claude Code appends one line per content block
// -- a thinking block and a tool_use block from the same reply are two lines
// carrying identical `message.id` and identical token counts. Summing the
// lines therefore over-counts, and by a lot: the first real cross-check here
// read 127,667 cache-read tokens against a `claude -p` envelope's 80,497 for
// the same turn, a 59% overstatement that would have been the decision
// record's headline number.
//
// So responses are deduplicated on message.id, and TestTranscriptTotalsMatch
// TheEnvelopeForTheSameTurn pins the result against real captured output from
// both sources for one real turn. That test is the reason any figure derived
// from a transcript in this program can be believed.
//
// A truncated final line is ignored rather than fatal: the file is being
// appended to by a live process while this reads it.
func parseTranscript(r io.Reader) ([]transcriptTurn, error) {
	var turns []transcriptTurn
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec transcriptRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // a partially written last line, or a record shape we do not read
		}
		switch rec.Type {
		case "user":
			if rec.IsSidechain || rec.IsMeta || len(rec.Message) == 0 {
				continue
			}
			var msg transcriptMessage
			if err := json.Unmarshal(rec.Message, &msg); err != nil {
				continue
			}
			var prompt string
			if err := json.Unmarshal(msg.Content, &prompt); err != nil {
				continue // content is an array => a tool result, not a prompt
			}
			turns = append(turns, transcriptTurn{Prompt: prompt})
		case "assistant":
			if len(turns) == 0 || len(rec.Message) == 0 {
				continue
			}
			var msg transcriptMessage
			if err := json.Unmarshal(rec.Message, &msg); err != nil {
				continue
			}
			if msg.Usage == nil {
				continue
			}
			// A response with no id cannot be deduplicated, so it is counted
			// once and no more: dropping it would under-report, and counting
			// every copy would over-report. There is no third option that does
			// not invent something.
			if msg.ID != "" {
				if seen[msg.ID] {
					continue
				}
				seen[msg.ID] = true
			}
			u := usage{
				Model:         msg.Model,
				StopReason:    msg.StopReason,
				Input:         msg.Usage.InputTokens,
				Output:        msg.Usage.OutputTokens,
				CacheRead:     msg.Usage.CacheReadInputTokens,
				CacheCreation: msg.Usage.CacheCreationInputTokens,
				Sidechain:     rec.IsSidechain,
			}
			if rec.Timestamp != "" {
				if ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
					u.At = ts
				}
			}
			turns[len(turns)-1].Calls = append(turns[len(turns)-1].Calls, u)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	return turns, nil
}

func parseTranscriptFile(path string) ([]transcriptTurn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseTranscript(f)
}
