package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestClientAgainstRealMCPServer speaks to agent-supervisor's actual
// mcp_server.py over stdio -- not a fake -- to prove this client's framing
// works against the real protocol, not an assumption about it. It is
// skipped when no supervisor checkout is available (this repo imports no
// supervisor internals and does not vendor one), which is why CI here
// cannot always run it; a human or the agent-supervisor CI is expected to
// exercise the pairing.
func TestClientAgainstRealMCPServer(t *testing.T) {
	repo := os.Getenv("AGENT_SUPERVISOR_REPO")
	if repo == "" {
		t.Skip("AGENT_SUPERVISOR_REPO not set; skipping the live MCP pairing test")
	}
	script := repo + "/scripts/supervisor/mcp_server.py"
	if _, err := os.Stat(script); err != nil {
		t.Skipf("mcp_server.py not found at %s: %v", script, err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}

	c, err := Start(python, script)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	text, err := c.CallTool("lanes", map[string]any{})
	if err != nil {
		// A session that does not exist is a legitimate answer from the
		// real server (SupervisorUnavailable), not a client bug -- this
		// still proves the framing round-tripped a real error.
		t.Logf("lanes tool returned an error (acceptable if no tmux session is up): %v", err)
		return
	}
	if text == "" {
		t.Fatal("lanes tool returned no text on success")
	}
}

// TestCallSurfacesErrorInsteadOfHangingPastDeadline is the fix for at#22's
// blocking finding's second half: a subprocess that stops responding (not
// crashes -- crashing was already handled, via readLoop's cleanup closing
// every pending channel) must surface as a visible, honest error within
// callTimeout, not hang forever. `sh -c "cat >/dev/null"` reads every byte
// this client writes to its stdin and discards it, never writing a reply to
// its stdout -- exactly "the subprocess is alive but silent", the one case
// the pre-fix bare `resp, ok := <-ch` had no defense against (plain `cat`
// would not do: it echoes stdin back to stdout, which this client would
// decode as a reply). callTimeout is shrunk for the duration of this test
// so it does not actually wait out a production-length deadline.
func TestCallSurfacesErrorInsteadOfHangingPastDeadline(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}

	orig := callTimeout
	callTimeout = 200 * time.Millisecond
	defer func() { callTimeout = orig }()

	start := time.Now()
	c, err := Start(sh, "-c", "cat >/dev/null")
	elapsed := time.Since(start)
	if c != nil {
		defer c.Close()
	}
	if err == nil {
		t.Fatal("Start against a subprocess that never replies returned no error")
	}
	if !strings.Contains(err.Error(), "no reply within") {
		t.Fatalf("error = %v, want a deadline error naming callTimeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Start took %s to fail, want it bounded by callTimeout (%s)", elapsed, callTimeout)
	}
}

// blockingWriter's Write never returns until the test releases entered --
// a stand-in for a real stdin pipe wedged by backpressure from the child's
// own stdout filling up (agent-tui#167's actual reproduction: a live
// mcp_server.py's write() blocked on a full pipe because nothing was
// draining its stdout). It signals entered exactly once, the moment a
// Write call is inside the block, so a test can deterministically wait
// for "a write is currently stuck" before asserting anything.
type blockingWriter struct {
	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.enteredOnce.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func (w *blockingWriter) Close() error { close(w.release); return nil }

// TestPendRegistrationNotBlockedByStuckWrite is the regression test for
// agent-tui#167's leak: reproduced live against a real agent-supervisor
// checkout, idle RSS climbed 80MB -> 106MB and runtime.NumGoroutine()-shaped
// evidence (a pprof goroutine dump) climbed 79 -> 1594 goroutines in under
// 20 minutes with nobody interacting (full sample series in the PR body).
// Root cause, found via two goroutine dumps 1 minute apart, diffed: the
// pre-fix callTimeout held ONE mutex (mu) across BOTH the pend-map
// registration AND the blocking c.stdin.Write call, and readLoop needed
// that SAME mu to deliver every response, even ones it had already fully
// read off the wire. A live supervisor's "sessions"/"lanes" tool response
// can be large or slow enough that mcp_server.py's own stdout write blocks
// on a full pipe while readLoop -- the one goroutine that would drain it --
// is stuck waiting for mu (held by whichever call is mid-Write). Once
// readLoop stops draining stdout, both pipes wedge permanently and every
// future 2-second refresh tick (chat/monitor/agents) leaks a fresh
// goroutine blocked on mu.Lock forever.
//
// This test isolates the exact defect without needing a real subprocess or
// readLoop: it proves pend registration (c.mu) is decoupled from a stuck
// stdin Write (c.writeMu) at all. Reverting the writeMu split in
// client.go's callTimeout (renaming writeMu back to mu for the Write call)
// makes this test hang until it times out and fails -- the mutation check
// this fix's PR body cites.
func TestPendRegistrationNotBlockedByStuckWrite(t *testing.T) {
	w := newBlockingWriter()
	c := &Client{
		// stdout is never touched by this test -- it isolates callTimeout's
		// pend-registration-vs-write lock ordering without needing a real
		// readLoop or subprocess. An empty reader is all that's required.
		stdin:   w,
		stdout:  bufio.NewReader(strings.NewReader("")),
		pend:    make(map[int64]chan rpcResponse),
		writeMu: make(chan struct{}, 1),
		callSem: make(chan struct{}, 1),
	}

	// Goroutine A: a call whose stdin Write blocks forever (simulating the
	// live pipe backpressure this bug needs to manifest). It registers its
	// own pend entry first, exactly like a real call, then wedges in Write.
	go func() {
		_, _ = c.callTimeout("stuck", nil, time.Hour)
	}()

	select {
	case <-w.entered:
		// A is now genuinely inside the blocked Write call, holding
		// writeMu (or, pre-fix, mu) for as long as this test runs.
	case <-time.After(2 * time.Second):
		t.Fatal("the stuck call never entered its Write -- test setup is broken, not the fix")
	}

	// The actual assertion: acquiring mu (what pend registration AND
	// readLoop's response delivery both need) must not be blocked by A's
	// in-flight Write. Pre-fix, this deadlocks because callTimeout held
	// the SAME mu across the Write call A is currently stuck inside.
	acquired := make(chan struct{})
	go func() {
		c.mu.Lock()
		c.mu.Unlock()
		close(acquired)
	}()

	select {
	case <-acquired:
		// Fixed: mu is free even while a stdin Write is permanently stuck.
	case <-time.After(2 * time.Second):
		t.Fatal("mu.Lock() blocked by a concurrent stdin Write -- this is agent-tui#167's leak: " +
			"readLoop needs this same lock to deliver responses and keep draining stdout, so a " +
			"single stuck write wedges the whole client and every future refresh tick leaks a goroutine forever")
	}

	// w.Close, not c.Close -- c.cmd/c.stderr are nil in this test (no real
	// subprocess), and c.Close dereferences both. Releasing the blockingWriter
	// is all that's needed to let goroutine A's stuck callTimeout return.
	_ = w.Close()
}

// TestFloodOfCallsDoesNotPileUpOnWriteMu is the regression test for
// agent-tui#171: the review that reopened agent-tui#167. That review
// reproduced the identical reproduction (a permanently non-reading child, a
// 200-call flood, 1s-deadline calls) against the writeMu split above and got
// the identical plateau -- 204 goroutines, still there 6s past every call's
// own deadline -- because mu was free but all 200 flood calls now piled up
// forever on writeMu.Lock() instead: the same unbounded-wait defect, one
// lock over. A sync.Mutex has no way to give up waiting once a caller's own
// context deadline passes, so a stuck writer pins every later caller
// indefinitely.
//
// This test does the same thing without a real subprocess: one goroutine
// (A) wedges inside stdin.Write forever, exactly like a child that stopped
// draining its own stdin. Then 200 goroutines (the flood) each call
// callTimeout with a short deadline, simulating 200 concurrent tool calls
// hitting the same wedged client. The assertion is that every flooded call
// returns once its own deadline passes -- goroutine count comes back down
// to baseline (plus the one goroutine A, which stays genuinely and
// permanently stuck inside the blocking Write syscall; there is no way to
// cancel an in-flight blocking write short of closing stdin) -- rather than
// sitting at a 200-goroutine plateau. Reverting the writeMu channel back to
// a sync.Mutex (client.go's callTimeout) makes this test time out with the
// flood goroutines never returning -- the mutation check this fix's PR body
// cites.
func TestFloodOfCallsDoesNotPileUpOnWriteMu(t *testing.T) {
	w := newBlockingWriter()
	c := &Client{
		stdin:   w,
		stdout:  bufio.NewReader(strings.NewReader("")),
		pend:    make(map[int64]chan rpcResponse),
		writeMu: make(chan struct{}, 1),
		callSem: make(chan struct{}, 1),
	}

	// Goroutine A: wedges forever inside stdin.Write, exactly like a real
	// child that stopped draining its own stdin pipe.
	go func() {
		_, _ = c.callTimeout("stuck", nil, time.Hour)
	}()
	select {
	case <-w.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the stuck call never entered its Write -- test setup is broken, not the fix")
	}

	// The flood: 200 concurrent calls, each with a short deadline, exactly
	// like agent-tui#171's own reproduction. Every one of them must
	// return -- with a timeout error, not a reply -- once its own deadline
	// passes rather than queuing on writeMu forever.
	const flood = 200
	const perCallDeadline = 200 * time.Millisecond
	done := make(chan struct{}, flood)
	for i := 0; i < flood; i++ {
		go func() {
			_, _ = c.callTimeout("flood", nil, perCallDeadline)
			done <- struct{}{}
		}()
	}

	deadline := time.After(perCallDeadline + 5*time.Second)
	returned := 0
	for returned < flood {
		select {
		case <-done:
			returned++
		case <-deadline:
			t.Fatalf("only %d/%d flooded calls returned within %s of their own %s deadline -- "+
				"this is agent-tui#171's leak: calls piling up on writeMu with no bound instead of "+
				"giving up once their own context deadline passes",
				returned, flood, perCallDeadline+5*time.Second, perCallDeadline)
		}
	}

	// w.Close, not c.Close -- see the identical note above.
	_ = w.Close()
}

// discardWriteCloser accepts every Write instantly and never blocks --
// unlike blockingWriter above, this models a write that succeeds (the
// child's stdin is being read fine) on a Client whose reply never arrives,
// so callTimeout fails via the reply-wait ctx.Done() branch rather than
// ever touching writeMu's own contention.
type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }

// TestCallSemReleasedAfterFailedCall is the regression test for the "do not
// leave the guard held on the error path" requirement in agent-tui#177's
// brief: callSem (client.go) must be released even when callTimeout returns
// an error, or one failing call permanently deadlocks every later caller on
// this Client -- strictly worse than the cross-pane timeout this fix closes.
// This uses a stdout that never produces a reply (an empty reader), so the
// call fails via the reply-wait ctx.Done() branch, then asserts the slot is
// immediately acquirable again rather than still held.
//
// Mutation check (agent-tui#177's brief, "break it deliberately and confirm
// a test goes red"): moving callSem's `defer func() { <-c.callSem }()` in
// client.go so it runs only on the success path (e.g. inlining the release
// at the end of the function body instead of via defer right after
// acquisition) makes this test fail with "callSem still held" -- confirmed
// by hand during this fix's own review, not left as an assertion nobody ran.
func TestCallSemReleasedAfterFailedCall(t *testing.T) {
	orig := callTimeout
	callTimeout = 50 * time.Millisecond
	defer func() { callTimeout = orig }()

	c := &Client{
		stdin:   discardWriteCloser{},
		stdout:  bufio.NewReader(strings.NewReader("")),
		pend:    make(map[int64]chan rpcResponse),
		writeMu: make(chan struct{}, 1),
		callSem: make(chan struct{}, 1),
	}

	_, err := c.callTimeout("nope", nil, callTimeout)
	if err == nil {
		t.Fatal("callTimeout against a Client whose stdout never replies returned no error")
	}

	select {
	case c.callSem <- struct{}{}:
		<-c.callSem
	default:
		t.Fatal("callSem still held after a failed call -- agent-tui#177's guard leaked its " +
			"serialisation slot on the error path, which deadlocks every future caller on this Client")
	}
}

// TestCallSemLimitsInFlightRequestsToOne is the cross-pane reproduction
// agent-tui#177 asks for: several concurrent CallTool-shaped calls through
// the SAME Client (exactly cmd/estate/main.go's shape -- internal/rail,
// internal/agents, internal/monitor and internal/chat's participants roster
// all share one sessionsFetch/lanesFetch closure over one *mcp.Client) must
// never have more than one request outstanding (written, awaiting a reply)
// at once.
//
// Deliberately NOT a wall-clock/elapsed-time assertion: the fake server
// below answers requests strictly one at a time regardless of client
// behaviour (its decode-sleep-reply loop is itself sequential), so total
// wall time for N calls is ~N*delay whether or not the CLIENT serialises --
// that shape of test was tried first and verified, by hand, to keep passing
// even with callSem's acquire/release deleted, which makes it useless as a
// regression test. What actually distinguishes "the client serialises" is
// how many requests are simultaneously registered in c.pend (written but
// not yet answered): callSem restricts this to at most 1; without it, up to
// N goroutines can each register a pend entry and write their request
// before any reply arrives, since writeMu only guards the brief Write call
// itself, not the wait that follows it.
//
// Mutation check (agent-tui#177's brief, "break it deliberately and confirm
// a test goes red"): deleting callSem's acquire/defer-release pair in
// client.go's callTimeout makes this test fail with "observed 5 requests
// simultaneously in flight" -- confirmed by hand during this fix's own
// review (a wall-clock version of this same mutation, tried first, did NOT
// go red, for the reason explained above).
func TestCallSemLimitsInFlightRequestsToOne(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	const delay = 100 * time.Millisecond
	go func() {
		dec := json.NewDecoder(stdinR)
		for {
			var req rpcRequest
			if err := dec.Decode(&req); err != nil {
				return
			}
			time.Sleep(delay)
			resp := rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
			}
			b, err := json.Marshal(resp)
			if err != nil {
				return
			}
			if _, err := stdoutW.Write(append(b, '\n')); err != nil {
				return
			}
		}
	}()

	c := &Client{
		stdin:   stdinW,
		stdout:  bufio.NewReader(stdoutR),
		pend:    make(map[int64]chan rpcResponse),
		writeMu: make(chan struct{}, 1),
		callSem: make(chan struct{}, 1),
	}
	go c.readLoop()

	const n = 5
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.callTimeout("sessions", nil, 2*time.Second)
		}()
	}

	// Sample mid-flight, once the first request is certainly already
	// registered and being "processed" (asleep in the fake server) but
	// before it can possibly have replied -- if callSem let every flood
	// goroutine past registration instead of queuing them, all 5 pend
	// entries would already exist by now.
	time.Sleep(delay / 2)
	c.mu.Lock()
	inFlight := len(c.pend)
	c.mu.Unlock()

	wg.Wait()

	if inFlight > 1 {
		t.Fatalf("observed %d requests simultaneously in flight through one Client, want at most 1 -- "+
			"agent-tui#177's callSem did not serialise cross-pane callers", inFlight)
	}
}
