// Package worker drives one long-lived, stream-json `claude` child across
// many turns -- the K6 slice-1 persistent worker (agent-estate#1222), scoped
// by the handoff and its amendment on agent-estate#1187. It owns exactly
// three things: process lifecycle, stdin ownership across idle gaps, and
// turn submission with response draining. Nothing else -- no scheduling, no
// queueing, no lane selection, no retry, no crash recovery, no
// resume-after-restart, and no change to `estate dispatch`'s own behaviour;
// all of those are explicitly out of scope for this slice (agent-estate#1222).
//
// THE TRANSPORT THIS PACKAGE USES IS THE ONE agent-estate#1187 MEASURED
// WORKING, not the one its first design proposed. A named pipe (FIFO) fed
// directly to the harness's own stdin was tried and refuted there: the
// child reads the bytes (a nonzero fd-0 offset under lsof) and then
// produces nothing and never exits, in both --print and default modes.
// What works, measured independently in that thread by two different
// people, is an ANONYMOUS pipe owned by this package's own parent process
// (os/exec's StdinPipe), held open across an idle gap the soak
// (agent-estate#1190) confirmed survives at least 180 minutes -- longer
// than both boundaries with engineering meaning: the 45-minute dispatch
// turn timeout and internal/mirror's 90-minute MaxAge. Never substitute a
// FIFO for the child's own stdin; that variant is refuted, not merely
// undesirable, and nothing in this package reopens that question.
//
// REUSE, NOT A SECOND PARSER. The child is spawned with `--input-format
// stream-json --output-format stream-json`, so its stdout is JSONL, one
// event per line -- but the final "result"-typed line of any one turn
// carries exactly the same envelope internal/harness.ClaudeResult,
// ClaudeSpend and ClaudeSessionID already parse for `claude -p
// --output-format json`, verified directly against a real run of this
// package's own invocation shape:
//
//	{"type":"result","subtype":"success","total_cost_usd":0.0967,
//	 "usage":{...},"modelUsage":{...},"session_id":"...","result":"OK", ...}
//
// So this package isolates that one line and hands its bytes to those three
// functions unchanged (see submit's use of them below). Writing a second
// stream-json parser here is exactly the drift agent-estate#1222 exists to
// prevent -- the whole point of this worker is to be one arm of a
// cost-and-quality comparison against ordinary dispatch, and two parsers
// for one envelope shape is how the two sides of that comparison stop being
// attributable to each other.
//
// ABSENCE IS A TYPED VALUE, NEVER A ZERO. A turn whose spend cannot be
// measured -- the child reported no usable result line, or the line it
// reported omitted a field -- records that turn's Spend fields as nil,
// never as 0. This is ledger.Record's own SpendCostUSD discipline, reused
// rather than reinvented; see appendTurn below and the negative tests in
// worker_test.go, which are the actual deliverable of this package, not the
// happy path.
//
// STALENESS WITHOUT A BLIND HEARTBEAT. internal/mirror's own Config.Heartbeat
// ticks on a timer regardless of what the child is doing -- exactly right
// for a single bounded dispatch turn (a harness that prints nothing until
// it finishes), and exactly wrong for a persistent worker, where it would
// mask a child that is alive but has stopped answering mid-turn. This
// package therefore disables mirror's own ticker (Heartbeat: 0) and runs
// its own, gated on whether a turn is actually in flight -- see
// idleHeartbeat and Stale's doc comments for the failure mode this avoids.
package worker

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/dispatchid"
	"github.com/jonhill90/agent-estate/estate/internal/harness"
	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/mirror"
)

// defaultMaxAge mirrors mirror.Default's own 90-minute figure -- reused as
// the fallback for Stale, not re-derived, so the two packages' notions of
// "how long is too long to hear nothing" cannot silently drift apart.
const defaultMaxAge = 90 * time.Minute

// defaultHeartbeat mirrors mirror.Default's own 15-second figure -- the
// cadence this package's own idle-only heartbeat uses when the caller's
// Mirror config did not set one.
const defaultHeartbeat = 15 * time.Second

var (
	// ErrBusy means a turn is already in flight. This slice deliberately
	// serialises Submit -- no scheduling, no queueing (see the package
	// doc) -- so a caller asking for a second concurrent turn is asking for
	// a capability this slice does not build, not hitting a bug.
	ErrBusy = errors.New("worker: a turn is already in flight")
	// ErrDead means the child process is already known to have exited.
	// Submit refuses immediately rather than writing to a closed pipe and
	// discovering the truth only once that write fails.
	ErrDead = errors.New("worker: the child process has already exited")
)

// Process is the child harness process a Worker drives across many turns.
// spawnProcess (bound to Spawn below) is the real implementation, wrapping
// os/exec around the claude binary with an anonymous pipe as its stdin.
// Every test in this package supplies a fake instead -- the brief is
// explicit that the unit suite must never spawn a real claude.
type Process interface {
	// Send writes one already newline-terminated JSONL line to the child's
	// stdin -- the anonymous pipe this package owns, never a FIFO the
	// child reads directly (see the package doc for why that shape is
	// refused).
	Send(line string) error
	// Lines is the channel the child's stdout is drained into, one JSONL
	// line at a time, closed when the child's stdout reaches EOF.
	Lines() <-chan string
	// Pid is the child's own process id -- recorded on every ledger record
	// this package writes, and handed to internal/mirror as
	// Config.OwnerPID, so mirror's own liveness check reflects the actual
	// worker process, never this package's wrapper goroutines.
	Pid() int
	// Wait blocks until the child exits and reports how.
	Wait() error
	// Close closes the child's stdin -- the clean-exit path a graceful
	// Stop uses, relying on the same "clean exit on stdin close" behaviour
	// agent-estate#1187's soak measured.
	Close() error
	// Kill terminates the child immediately, for a Stop whose timeout
	// elapsed before the child exited on its own.
	Kill() error
}

// execProcess is the real Process, one os/exec child with its stdin held
// open as an anonymous pipe across idle gaps.
type execProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string

	mu sync.Mutex // serialises writes to stdin
}

// spawnProcess starts the real persistent child:
// `claude --print --verbose --input-format stream-json --output-format
// stream-json --model <m> --session-id <uuid>`, exactly the invocation
// measured working in agent-estate#1187.
func spawnProcess(ctx context.Context, dir, model, sessionID string) (Process, error) {
	cmd := exec.CommandContext(ctx, "claude",
		"--print", "--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--model", model,
		"--session-id", sessionID,
	)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("worker: cannot open the child's stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("worker: cannot open the child's stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("worker: cannot start claude: %w", err)
	}
	p := &execProcess{cmd: cmd, stdin: stdin, lines: make(chan string, 16)}
	go func() {
		defer close(p.lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
	}()
	return p, nil
}

func (p *execProcess) Send(line string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := io.WriteString(p.stdin, line)
	return err
}

func (p *execProcess) Lines() <-chan string { return p.lines }

func (p *execProcess) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *execProcess) Wait() error { return p.cmd.Wait() }

func (p *execProcess) Close() error { return p.stdin.Close() }

func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// Spawn starts the real child. A package variable, not a bare function
// reference baked into Config's zero value, so a caller that genuinely
// wants the real binary gets it by doing nothing, while the unit suite
// points Config.Spawn at a fake and never touches this at all.
var Spawn = spawnProcess

// Config is everything a caller supplies to start one persistent worker.
type Config struct {
	// Issue names the ledger issue field for both the worker's own lane
	// record and every turn record it produces -- the same field a
	// dispatched turn already carries.
	Issue string
	// Model is the harness model id, required for the same reason
	// internal/harness's claude adapter requires it explicitly: without it
	// a worker silently inherits whatever model happens to be ambient
	// (see harness.claude.Start's own doc comment for the incident this
	// guards against).
	Model string
	// Dir is the child's working directory.
	Dir string
	// Ledger is where the worker's own lifetime record and every turn
	// record are appended. Required.
	Ledger *ledger.Ledger
	// Mirror is the transcript/tmux configuration the worker's transcript
	// is opened with, on the same terms (Session, Max, Keep, MaxAge) as a
	// dispatch's own -- see internal/mirror.Default. OwnerPID is
	// overwritten with the child's own pid once it is known, and Heartbeat
	// is forced to 0: see the package doc's staleness section for why a
	// blind ticker is refused here.
	Mirror mirror.Config
	// Spawn starts the child. Nil means Spawn (the real binary); tests
	// supply a fake so the unit suite never spawns a real claude.
	Spawn func(ctx context.Context, dir, model, sessionID string) (Process, error)
	// NewSessionID mints the lane's own identity. --session-id requires a
	// genuine UUID (`claude --help`), which internal/dispatchid's
	// pid+nanos+seq scheme does not produce -- agent-estate#1187's first
	// design revision made exactly this mistake and corrected it in the
	// second. Nil means a real random UUID v4 (newUUID below).
	NewSessionID func() string
	// Now is the clock, injectable for tests. Nil means time.Now.
	Now func() time.Time
}

// Result is one turn's outcome.
type Result struct {
	// Text is the harness's own final message for this turn.
	Text string
	// Spend is what the harness itself reported this turn cost -- absent
	// fields are nil, never zero. See internal/harness.Spend's own doc
	// comment for the discipline this reuses.
	Spend harness.Spend
	// SessionID is the harness's own conversation handle, expected to
	// equal the worker's own lane id on every turn -- carried on Result so
	// a caller can assert that without re-deriving it.
	SessionID string
}

// Worker is one persistent child, driven one turn at a time.
type Worker struct {
	cfg       Config
	sessionID string
	proc      Process
	mir       *mirror.Mirror

	mu            sync.Mutex
	busy          bool
	dead          bool
	deadReason    string
	stopRequested bool
	lastActivity  time.Time

	exited       chan struct{}
	finalizeOnce sync.Once
}

// Start mints the worker's own lane id, spawns the child, opens its mirror,
// and appends the worker's own ledger record (State=Dispatched -- the same
// process-launched meaning a dispatched turn's own Dispatched record
// carries, so estate inflight and estate reclaim see this worker exactly as
// they see a stranded dispatch, with no changes needed to either).
//
// A spawn failure appends a terminal Failed record for the lane id that was
// minted (never left dangling as though nothing was attempted) and returns
// an error; the caller has nothing further to clean up.
func Start(ctx context.Context, cfg Config) (*Worker, error) {
	if cfg.Ledger == nil {
		return nil, errors.New("worker: no ledger supplied")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("worker: no model supplied -- a worker must never inherit an ambient model")
	}
	if cfg.Spawn == nil {
		cfg.Spawn = Spawn
	}
	if cfg.NewSessionID == nil {
		cfg.NewSessionID = newUUID
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	sid := cfg.NewSessionID()
	proc, err := cfg.Spawn(ctx, cfg.Dir, cfg.Model, sid)
	if err != nil {
		cfg.Ledger.Append(ledger.Record{
			ID:      sid,
			Issue:   cfg.Issue,
			Lane:    sid,
			State:   ledger.Failed,
			Harness: "claude",
			Note:    "worker failed to start: " + err.Error(),
		})
		return nil, fmt.Errorf("worker: cannot start persistent child: %w", err)
	}

	mcfg := cfg.Mirror
	mcfg.OwnerPID = proc.Pid()
	// This package runs its own idle-gated heartbeat (idleHeartbeat) rather
	// than mirror's blind ticker -- see the package doc.
	mcfg.Heartbeat = 0
	mir, _ := mirror.Open(mcfg, mirror.Meta{
		ID:       sid,
		Issue:    cfg.Issue,
		Role:     "worker",
		Harness:  "claude",
		Worktree: cfg.Dir,
	})
	// A tmux failure is not fatal here, same as it is not fatal to a
	// dispatch: mirror.Open's own doc comment says the caller must proceed
	// unmirrored rather than refuse to run because a screen could not be
	// opened, and every Mirror method is safe to call on a nil receiver.

	if err := cfg.Ledger.Append(ledger.Record{
		ID:      sid,
		Issue:   cfg.Issue,
		Lane:    sid,
		State:   ledger.Dispatched,
		PID:     proc.Pid(),
		Harness: "claude",
		Note:    "persistent worker started",
	}); err != nil {
		mir.Close(string(ledger.Failed), "could not record the worker's own start")
		proc.Kill()
		return nil, fmt.Errorf("worker: cannot append the worker's own ledger record: %w", err)
	}

	w := &Worker{
		cfg:          cfg,
		sessionID:    sid,
		proc:         proc,
		mir:          mir,
		lastActivity: cfg.Now(),
		exited:       make(chan struct{}),
	}
	go w.watchExit()
	go w.autoFinalizeOnUnexpectedExit()
	go w.idleHeartbeat()
	return w, nil
}

// SessionID is the worker's own lane identity -- the harness's conversation
// handle, minted before the first turn ever runs and expected to be reused,
// unchanged, on every SessionID this worker's turns ever report back.
func (w *Worker) SessionID() string { return w.sessionID }

// PID is the child harness process's own pid.
func (w *Worker) PID() int { return w.proc.Pid() }

// Dead reports whether the child is known to have exited, and why.
func (w *Worker) Dead() (bool, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dead, w.deadReason
}

// Stale reports whether the worker has gone quiet for longer than maxAge,
// evaluated at now -- the same MaxAge concept internal/mirror uses for its
// own backstop (mirror.Config.MaxAge is "twice the caller's turn timeout"
// for a bounded dispatch; a persistent worker has no bounded turn, so the
// caller supplies whatever interval it wants treated as suspicious).
// maxAge<=0 defaults to defaultMaxAge, mirror.Default's own 90 minutes.
//
// This is deliberately NOT computed by re-reading the mirror transcript's
// mtime. This package's own idle heartbeat (idleHeartbeat) is SUPPRESSED
// while a turn is in flight, precisely so a child that stops answering
// mid-turn is not masked by a heartbeat that ticks blindly regardless of
// whether the child is actually making progress -- see the package doc's
// staleness section. lastActivity is this package's own record of the last
// moment it had positive evidence (real output from the child, or a
// genuinely idle tick) that the worker was progressing; Stale reads that
// record rather than the filesystem.
func (w *Worker) Stale(now time.Time, maxAge time.Duration) bool {
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dead {
		return true
	}
	return now.Sub(w.lastActivity) > maxAge
}

// watchExit blocks on the child's own exit and records why, but does not
// itself write to the ledger -- see finalize and autoFinalizeOnUnexpectedExit
// for why that write happens exactly once, from exactly one place.
func (w *Worker) watchExit() {
	err := w.proc.Wait()
	reason := "child exited"
	if err != nil {
		reason = "child exited: " + err.Error()
	}
	w.mu.Lock()
	if !w.dead {
		w.dead = true
		w.deadReason = reason
	}
	w.mu.Unlock()
	close(w.exited)
}

// autoFinalizeOnUnexpectedExit writes the worker's own terminal ledger
// record the moment the child exits WITHOUT a Stop having been requested --
// the N1 case (child dies mid-turn, or between turns): a terminal record
// with a reason, never a silent hang. If Stop was requested, Stop itself
// writes the terminal record (as Complete, a graceful shutdown), and this
// goroutine defers to it.
func (w *Worker) autoFinalizeOnUnexpectedExit() {
	<-w.exited
	w.mu.Lock()
	stopReq := w.stopRequested
	reason := w.deadReason
	w.mu.Unlock()
	if stopReq {
		return
	}
	w.finalize(ledger.Failed, reason)
}

// finalize appends the worker's own terminal ledger record and closes its
// mirror. Guarded by sync.Once so a race between Stop and an unexpected
// exit can never double-write.
func (w *Worker) finalize(state ledger.State, note string) {
	w.finalizeOnce.Do(func() {
		w.mir.Close(string(state), note)
		w.cfg.Ledger.Append(ledger.Record{
			ID:      w.sessionID,
			Issue:   w.cfg.Issue,
			Lane:    w.sessionID,
			State:   state,
			PID:     w.proc.Pid(),
			Harness: "claude",
			Note:    note,
		})
	})
}

// Stop asks the worker to end gracefully: it closes the child's stdin
// (clean exit on EOF, per the soak evidence) and waits up to timeout for
// the process to exit on its own before killing it. Exactly one terminal
// ledger record is appended for the worker's own lane id -- Complete for
// this, the graceful path -- reusing the states dispatched turns already
// use rather than inventing a new one.
func (w *Worker) Stop(timeout time.Duration) {
	w.mu.Lock()
	w.stopRequested = true
	w.mu.Unlock()

	w.proc.Close()
	select {
	case <-w.exited:
	case <-time.After(timeout):
		w.proc.Kill()
		<-w.exited
	}
	w.finalize(ledger.Complete, "worker stopped")
}

// idleHeartbeat keeps the transcript visibly alive between turns -- a human
// watching via tmux must be able to tell "still here, waiting" from "gone
// silent" -- without masking a mid-turn hang. It only writes, and only
// advances lastActivity, when the worker is not currently mid-Submit; see
// Stale's doc comment for why that gating is the whole point.
func (w *Worker) idleHeartbeat() {
	interval := w.cfg.Mirror.Heartbeat
	if interval <= 0 {
		interval = defaultHeartbeat
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-w.exited:
			return
		case <-t.C:
			w.mu.Lock()
			idle := !w.busy && !w.dead
			if idle {
				w.lastActivity = w.cfg.Now()
			}
			w.mu.Unlock()
			if idle {
				w.mir.Logf("worker idle, waiting for the next task (session %s)", w.sessionID)
			}
		}
	}
}

// Submit sends one task to the child and blocks until its result line
// arrives, ctx ends, or the child's stdout ends first. Exactly one turn may
// be in flight at a time (ErrBusy otherwise) -- this slice serialises
// feeding by construction; see the package doc.
func (w *Worker) Submit(ctx context.Context, task string) (Result, error) {
	w.mu.Lock()
	if w.dead {
		w.mu.Unlock()
		return Result{}, ErrDead
	}
	if w.busy {
		w.mu.Unlock()
		return Result{}, ErrBusy
	}
	w.busy = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.busy = false
		w.mu.Unlock()
	}()

	turnID := dispatchid.New(w.cfg.Issue, w.cfg.Now())

	line, err := userMessageLine(task)
	if err != nil {
		return Result{}, fmt.Errorf("worker: cannot build the turn's input line: %w", err)
	}

	if err := w.proc.Send(line); err != nil {
		w.markDead("could not write to the child's stdin: " + err.Error())
		w.appendTurn(turnID, ledger.Unknown, harness.Spend{}, "",
			fmt.Sprintf("could not submit the task: %v", err))
		return Result{}, fmt.Errorf("worker: could not submit the task: %w", err)
	}
	w.mir.Logf("[worker] submitted a task (turn %s)", turnID)

	for {
		select {
		case <-ctx.Done():
			// The worker may still be working on it -- we gave up
			// waiting, which is not the same claim as the worker having
			// died. Recorded Unknown, never Complete and never Failed:
			// this package did not observe an answer and must not
			// fabricate one either way.
			w.appendTurn(turnID, ledger.Unknown, harness.Spend{}, "",
				"the caller's context ended before a result arrived -- the worker may still be working on it")
			return Result{}, fmt.Errorf("worker: %w", ctx.Err())

		case raw, ok := <-w.proc.Lines():
			if !ok {
				reason := "child process ended before producing a result for this turn"
				w.markDead(reason)
				w.appendTurn(turnID, ledger.Failed, harness.Spend{}, "", reason)
				return Result{}, errors.New("worker: " + reason)
			}
			w.mir.Logf("%s", raw)
			w.mu.Lock()
			w.lastActivity = w.cfg.Now()
			w.mu.Unlock()
			if !isResultLine(raw) {
				continue
			}
			text, rerr := harness.ClaudeResult([]byte(raw))
			// Spend and SessionID are read the same unconditional way
			// Result is: an error from either means "not reported", never
			// a reason to fail a turn whose own result parsed fine. See
			// harness.Spend's own doc comment for the pointer-means-absent
			// discipline this reuses without reimplementing.
			sp, _ := harness.ClaudeSpend([]byte(raw))
			sid, _ := harness.ClaudeSessionID([]byte(raw))
			if rerr != nil {
				w.appendTurn(turnID, ledger.Unknown, sp, sid,
					"result line could not be parsed: "+rerr.Error())
				return Result{}, fmt.Errorf("worker: %w", rerr)
			}
			w.appendTurn(turnID, ledger.Complete, sp, sid, "")
			return Result{Text: text, Spend: sp, SessionID: sid}, nil
		}
	}
}

// markDead latches the in-memory dead flag so a subsequent Submit refuses
// immediately with ErrDead rather than hanging on a pipe that is already
// known to be closed. It never writes to the ledger itself -- that
// happens exactly once, from finalize, driven by the child's own Wait()
// returning (watchExit / autoFinalizeOnUnexpectedExit), so a fast
// in-memory latch here can never race a durable double-write.
func (w *Worker) markDead(reason string) {
	w.mu.Lock()
	if !w.dead {
		w.dead = true
		w.deadReason = reason
	}
	w.mu.Unlock()
}

// appendTurn writes one turn's own ledger record, Lane pointing at the
// worker's lane id (never the turn's own identity written there -- this
// package mints no shared use of Lane the way agent-estate#1187's review
// flagged for gate.go's Lane==ID assumption, because these records never
// carry Role=author/reviewer and gate.go never reads them). A write failure
// is logged to the mirror rather than failing an already-computed result:
// the harness's own answer, if any, is still valid even if this package
// could not durably record it.
func (w *Worker) appendTurn(turnID string, state ledger.State, sp harness.Spend, sessionID, note string) {
	rec := ledger.Record{
		ID:                       turnID,
		Issue:                    w.cfg.Issue,
		Lane:                     w.sessionID,
		State:                    state,
		PID:                      w.proc.Pid(),
		Harness:                  "claude",
		Note:                     note,
		SpendCostUSD:             sp.CostUSD,
		SpendInputTokens:         sp.InputTokens,
		SpendOutputTokens:        sp.OutputTokens,
		SpendCacheReadTokens:     sp.CacheReadTokens,
		SpendCacheCreationTokens: sp.CacheCreationTokens,
	}
	if len(sp.ByModel) > 0 {
		rec.SpendByModel = make(map[string]ledger.ModelSpend, len(sp.ByModel))
		for model, ms := range sp.ByModel {
			rec.SpendByModel[model] = ledger.ModelSpend{
				CostUSD:             ms.CostUSD,
				InputTokens:         ms.InputTokens,
				OutputTokens:        ms.OutputTokens,
				CacheReadTokens:     ms.CacheReadTokens,
				CacheCreationTokens: ms.CacheCreationTokens,
			}
		}
	}
	if sessionID != "" {
		rec.SessionID = &sessionID
	}
	if err := w.cfg.Ledger.Append(rec); err != nil {
		w.mir.Logf("[worker] could not record turn %s: %v", turnID, err)
	}
}

// --- stream-json input envelope -------------------------------------------

// streamJSONUserLine is the one input shape this package ever writes to the
// child's stdin -- a single-turn user message, plain text content, nothing
// else. Verified directly against a real `claude --input-format stream-json`
// run before being relied on here.
type streamJSONUserLine struct {
	Type    string            `json:"type"`
	Message streamJSONMessage `json:"message"`
}

type streamJSONMessage struct {
	Role    string              `json:"role"`
	Content []streamJSONContent `json:"content"`
}

type streamJSONContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func userMessageLine(task string) (string, error) {
	line := streamJSONUserLine{
		Type: "user",
		Message: streamJSONMessage{
			Role:    "user",
			Content: []streamJSONContent{{Type: "text", Text: task}},
		},
	}
	b, err := json.Marshal(line)
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// isResultLine reports whether raw is the one JSONL line per turn that
// carries the envelope harness.ClaudeResult/ClaudeSpend/ClaudeSessionID
// parse -- claude --output-format stream-json's "type":"result" line.
// Every other line (system, assistant, rate_limit_event, ...) is teed to
// the mirror for observability but is not a turn's own answer.
func isResultLine(raw string) bool {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return false
	}
	return probe.Type == "result"
}

// newUUID mints a random UUID v4 -- what --session-id requires (`claude
// --help`: "a valid UUID"), which internal/dispatchid's pid+nanos+seq
// format is not (agent-estate#1187's own correction). No third-party
// dependency: RFC 4122 needs four bits set in the version nibble and two in
// the variant nibble, nothing more.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not a condition this package can recover
		// from usefully -- there is no fallback id that would still be a
		// valid UUID, and --session-id would refuse anything else anyway.
		panic("worker: crypto/rand unavailable: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
