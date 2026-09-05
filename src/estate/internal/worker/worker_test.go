package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/mirror"
	"github.com/jonhill90/agent-estate/estate/internal/reclaim"
)

// --- test doubles ----------------------------------------------------------

// fakeProcess is the Process this whole suite drives instead of a real
// claude binary -- the brief is explicit that the unit suite must never
// spawn one. Every method is exactly the seam production code uses; test
// bodies control what the "child" does by pushing lines onto lines
// (buffered, so a line can be queued before Submit's own select loop is
// even reached) and by calling exit to simulate the process ending.
type fakeProcess struct {
	mu      sync.Mutex
	sent    []string
	lines   chan string
	pid     int
	sendErr error

	waitCh chan error
	waited sync.Once
}

func newFakeProcess(pid int) *fakeProcess {
	return &fakeProcess{
		lines:  make(chan string, 32),
		pid:    pid,
		waitCh: make(chan error, 1),
	}
}

func (f *fakeProcess) Send(line string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, line)
	return nil
}

func (f *fakeProcess) Lines() <-chan string { return f.lines }
func (f *fakeProcess) Pid() int             { return f.pid }
func (f *fakeProcess) Wait() error          { return <-f.waitCh }

// Close simulates the real, measured "clean exit on stdin close" behaviour
// (agent-estate#1187's soak): a graceful Stop closes stdin and the child
// exits rc=0.
func (f *fakeProcess) Close() error {
	f.waited.Do(func() {
		close(f.lines)
		f.waitCh <- nil
	})
	return nil
}

// Kill simulates a forced termination -- Stop's timeout path.
func (f *fakeProcess) Kill() error {
	f.waited.Do(func() {
		f.waitCh <- errors.New("killed")
	})
	return nil
}

// exit simulates the child ending on its own, unasked -- a crash or an
// unexpected exit mid-turn. It closes lines (so a pending Submit sees EOF)
// and delivers err to whatever Wait() call is in flight.
func (f *fakeProcess) exit(err error) {
	f.waited.Do(func() {
		close(f.lines)
		f.waitCh <- err
	})
}

func (f *fakeProcess) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// fakeClock is an injectable, manually-advanced clock -- Config.Now.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// testLedger opens a ledger at a fresh path under t.TempDir().
func testLedger(t *testing.T) *ledger.Ledger {
	t.Helper()
	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatalf("opening test ledger: %v", err)
	}
	return l
}

// startTestWorker wires a Worker to proc and l with mirroring disabled --
// this suite tests the ledger/process contract, not tmux, which
// internal/mirror's own tests already cover in isolation.
func startTestWorker(t *testing.T, proc *fakeProcess, l *ledger.Ledger, clock *fakeClock, sid string) *Worker {
	t.Helper()
	cfg := Config{
		Issue:  "9999",
		Model:  "claude-sonnet-5",
		Dir:    t.TempDir(),
		Ledger: l,
		Mirror: mirror.Config{Enabled: false},
		Spawn: func(ctx context.Context, dir, model, sessionID string) (Process, error) {
			return proc, nil
		},
		NewSessionID: func() string { return sid },
		Now:          clock.Now,
	}
	w, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { proc.exit(nil) })
	return w
}

// resultLine builds the one JSONL line this package's own doc comment says
// carries the whole harness.ClaudeResult/ClaudeSpend/ClaudeSessionID
// envelope -- shaped exactly like the real line captured against a live
// `claude --input-format stream-json --output-format stream-json` run.
func resultLine(sessionID, result string, costUSD *float64) string {
	cost := "null"
	if costUSD != nil {
		cost = fmt.Sprintf("%v", *costUSD)
	}
	return fmt.Sprintf(
		`{"type":"result","subtype":"success","total_cost_usd":%s,`+
			`"usage":{"input_tokens":2,"output_tokens":4},`+
			`"session_id":%q,"result":%q}`,
		cost, sessionID, result)
}

func floatp(f float64) *float64 { return &f }

func recordByID(t *testing.T, l *ledger.Ledger, id string) (ledger.Record, bool) {
	t.Helper()
	cur, err := l.Current()
	if err != nil {
		t.Fatalf("ledger.Current: %v", err)
	}
	for _, r := range cur {
		if r.ID == id {
			return r, true
		}
	}
	return ledger.Record{}, false
}

func waitForRecordState(t *testing.T, l *ledger.Ledger, id string, want ledger.State) ledger.Record {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := recordByID(t, l, id); ok && r.State == want {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("record %s never reached state %s", id, want)
	return ledger.Record{}
}

// --- positive tests ---------------------------------------------------

const testSID = "22222222-3333-4444-5555-666666666666"

func TestStart_WritesADispatchedRecordVisibleToInFlight(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(4242)
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	inflight, err := l.InFlight()
	if err != nil {
		t.Fatalf("InFlight: %v", err)
	}
	found := false
	for _, r := range inflight {
		if r.ID == w.SessionID() {
			found = true
			if r.State != ledger.Dispatched {
				t.Errorf("worker record state = %s, want Dispatched", r.State)
			}
			if r.PID != 4242 {
				t.Errorf("worker record PID = %d, want 4242", r.PID)
			}
		}
	}
	if !found {
		t.Fatalf("estate inflight does not see the running worker's own record")
	}
}

// P1: two turns through one worker reuse one session id, and the second
// turn's answer depends on the first -- simulated here by the test itself
// supplying turn 2's answer from what turn 1's own task said, which is the
// most a fake process can prove: that this package threads a turn's own
// task text to the child and reads back whatever comes out, rather than
// caching or rewriting either. Real context retention across an idle gap is
// the harness's own behaviour, independently measured against a live binary
// in agent-estate#1187 (180-minute soak, agent-estate#1190) -- not
// something a fake can honestly re-prove.
func TestSubmit_TwoTurnsReuseOneSessionID(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(4242)
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	proc.lines <- resultLine(testSID, "OK", floatp(0.01))
	r1, err := w.Submit(context.Background(), "remember 7331, reply OK")
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if r1.SessionID != testSID {
		t.Errorf("turn 1 session id = %q, want %q", r1.SessionID, testSID)
	}

	proc.lines <- resultLine(testSID, "7331", floatp(0.02))
	r2, err := w.Submit(context.Background(), "what number did I ask you to remember?")
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if r2.SessionID != testSID {
		t.Errorf("turn 2 session id = %q, want %q (SAME session as turn 1)", r2.SessionID, testSID)
	}
	if r2.Text != "7331" {
		t.Errorf("turn 2 result = %q", r2.Text)
	}

	cur, err := l.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	turns := 0
	for _, rec := range cur {
		if rec.ID == w.SessionID() {
			continue // the worker's own lane record, not a turn
		}
		if rec.Lane != w.SessionID() {
			continue
		}
		turns++
		if rec.State != ledger.Complete {
			t.Errorf("turn record %s state = %s, want Complete", rec.ID, rec.State)
		}
	}
	if turns != 2 {
		t.Fatalf("found %d turn records under this lane, want 2", turns)
	}
}

// P2: per-turn spend is recorded per turn, and sums to what the harness
// reported across both turns.
func TestSubmit_PerTurnSpendSumsToLifetimeTotal(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(4242)
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	proc.lines <- resultLine(testSID, "OK", floatp(0.10))
	if _, err := w.Submit(context.Background(), "task 1"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	proc.lines <- resultLine(testSID, "OK", floatp(0.25))
	if _, err := w.Submit(context.Background(), "task 2"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	cur, err := l.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	var sum float64
	n := 0
	for _, rec := range cur {
		if rec.ID == w.SessionID() || rec.Lane != w.SessionID() {
			continue
		}
		if rec.SpendCostUSD == nil {
			t.Fatalf("turn %s has no recorded spend", rec.ID)
		}
		sum += *rec.SpendCostUSD
		n++
	}
	if n != 2 {
		t.Fatalf("found %d turns, want 2", n)
	}
	if want := 0.35; sum < want-0.0001 || sum > want+0.0001 {
		t.Errorf("per-turn spend summed to %v, want %v", sum, want)
	}
}

// P3: visible to estate inflight while running, terminal after stop.
func TestStop_RecordBecomesTerminal(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(4242)
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	w.Stop(time.Second)

	rec := waitForRecordState(t, l, w.SessionID(), ledger.Complete)
	if rec.Note == "" {
		t.Errorf("stopped worker record carries no note")
	}
	inflight, err := l.InFlight()
	if err != nil {
		t.Fatalf("InFlight: %v", err)
	}
	for _, r := range inflight {
		if r.ID == w.SessionID() {
			t.Errorf("stopped worker's record is still reported in flight")
		}
	}
}

// --- negative tests: the deliverable, not the trimming ---------------------

// N1: child dies mid-turn -> a terminal ledger record with a reason, never
// a silent hang and never a fabricated answer.
func TestSubmit_ChildDiesMidTurn(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(4242)
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	before, err := l.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range before {
		seen[r.ID] = true
	}

	done := make(chan struct{})
	var res Result
	var subErr error
	go func() {
		res, subErr = w.Submit(context.Background(), "a task the child never answers")
		close(done)
	}()

	// Wait for Submit to have actually written to the child before killing
	// it, so this exercises "died mid-turn", not "died before dispatch".
	deadline := time.Now().Add(time.Second)
	for proc.sentCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if proc.sentCount() == 0 {
		t.Fatalf("Submit never wrote the task to the child")
	}
	proc.exit(errors.New("signal: killed"))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Submit hung instead of failing loudly when the child died mid-turn")
	}

	if subErr == nil {
		t.Fatalf("Submit returned no error for a child that died mid-turn")
	}
	if res.Text != "" {
		t.Errorf("Submit fabricated an answer (%q) for a child that died mid-turn", res.Text)
	}

	// Find the new turn record (the one record that did not exist before).
	after, err := l.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	var turn ledger.Record
	found := false
	for _, r := range after {
		if r.ID == w.SessionID() || seen[r.ID] {
			continue
		}
		turn, found = r, true
	}
	if !found {
		t.Fatalf("no per-turn ledger record was written for the turn in flight when the child died")
	}
	if turn.State != ledger.Failed {
		t.Errorf("dead-mid-turn record state = %s, want Failed", turn.State)
	}
	if turn.Result != "" {
		t.Errorf("dead-mid-turn record carries a fabricated Result: %q", turn.Result)
	}
	if turn.Note == "" {
		t.Errorf("dead-mid-turn record carries no reason")
	}

	// The worker's own lane record must also become terminal -- never a
	// silent hang at Dispatched forever.
	worker := waitForRecordState(t, l, w.SessionID(), ledger.Failed)
	if worker.Note == "" {
		t.Errorf("worker's own terminal record carries no reason")
	}
}

// N2: worker stops answering but the process lives -- a stale worker must
// not be read as alive. This package's own Stale (the MaxAge-shaped
// backstop, reusing internal/mirror's concept rather than reinventing a
// second one) must say so once enough quiet time has passed, and Submit
// giving up on a context deadline must never be recorded as success.
func TestSubmit_StopsAnsweringButProcessLives(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(4242) // never sends a line, never exits
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	res, err := w.Submit(ctx, "a task the child will never answer")
	if err == nil {
		t.Fatalf("Submit did not report the unresponsive child")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Submit error = %v, want context.DeadlineExceeded wrapped", err)
	}
	if res.Text != "" {
		t.Errorf("Submit fabricated an answer (%q) for a child that never answered", res.Text)
	}

	// The turn record must be neither Complete nor Failed -- this package
	// never observed an answer either way.
	after, err := l.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	var turn ledger.Record
	found := false
	for _, r := range after {
		if r.ID != w.SessionID() {
			turn, found = r, true
		}
	}
	if !found {
		t.Fatalf("no turn record was written for the unanswered submission")
	}
	if turn.State != ledger.Unknown {
		t.Errorf("unanswered-turn record state = %s, want Unknown", turn.State)
	}

	// Because the turn never completed, this package's own idle heartbeat
	// never had a chance to run either (Submit held w.busy the whole time),
	// so lastActivity is still wherever Start left it. A stale transcript
	// must not be read as alive: enough simulated quiet time must trip
	// Stale, the same MaxAge-shaped concept internal/mirror's own backstop
	// uses.
	clock.Advance(2 * time.Hour)
	if !w.Stale(clock.Now(), time.Hour) {
		t.Errorf("Stale = false after 2h of simulated quiet against a 1h MaxAge -- a wedged worker is being read as alive")
	}
}

// N3: a worker left in flight is reported by estate reclaim and freed by
// --apply, exactly as a stranded dispatch is. This is a wiring proof, not a
// new mechanism: it demonstrates that a worker's own ledger record (a plain
// Dispatched record with a real PID, Kind unchanged) already satisfies
// internal/reclaim's existing contract with zero changes to that package.
func TestWorkerRecord_ReclaimableExactlyAsAStrandedDispatchIs(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(999999) // a pid the fake probe below reports gone
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	inflight, err := l.InFlight()
	if err != nil {
		t.Fatalf("InFlight: %v", err)
	}
	var rec ledger.Record
	found := false
	for _, r := range inflight {
		if r.ID == w.SessionID() {
			rec, found = r, true
		}
	}
	if !found {
		t.Fatalf("the worker's own record is not in flight -- reclaim has nothing to assess")
	}

	deadProbe := func(pid int) (reclaim.ProcessInfo, error) {
		return reclaim.ProcessInfo{Exists: false}, nil
	}
	assessment := reclaim.Assess(rec, time.Time{}, deadProbe)
	if !assessment.Reclaimable {
		t.Fatalf("reclaim.Assess did not consider the stranded worker reclaimable: %s", assessment.Reason)
	}

	if _, err := reclaim.Apply(l, []reclaim.Assessment{assessment}); err != nil {
		t.Fatalf("reclaim.Apply: %v", err)
	}

	inflight, err = l.InFlight()
	if err != nil {
		t.Fatalf("InFlight after Apply: %v", err)
	}
	for _, r := range inflight {
		if r.ID == w.SessionID() {
			t.Fatalf("worker record still in flight after reclaim --apply")
		}
	}
}

// N4: spend unavailable for a turn -- the field is absent/typed-unknown,
// never zero. Absence is a typed value, never a bare zero: this is the one
// property that decides whether the eventual cost comparison can be
// trusted at all.
func TestSubmit_SpendUnavailableRecordsAbsenceNeverZero(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(4242)
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	// A result line the harness might genuinely produce: no total_cost_usd,
	// no usage block at all.
	line := fmt.Sprintf(`{"type":"result","subtype":"success","session_id":%q,"result":"OK"}`, testSID)
	proc.lines <- line

	res, err := w.Submit(context.Background(), "a task")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Spend.CostUSD != nil {
		t.Errorf("Result.Spend.CostUSD = %v, want nil (absent, not zero)", *res.Spend.CostUSD)
	}

	after, err := l.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	var turn ledger.Record
	found := false
	for _, r := range after {
		if r.ID != w.SessionID() {
			turn, found = r, true
		}
	}
	if !found {
		t.Fatalf("no turn record found")
	}
	if turn.SpendCostUSD != nil {
		t.Errorf("ledger record SpendCostUSD = %v, want nil -- absence must never be recorded as a bare zero", *turn.SpendCostUSD)
	}
	if turn.State != ledger.Complete {
		t.Errorf("state = %s, want Complete -- a turn that answered but reported no spend is still a completed turn", turn.State)
	}
}

// N4b: a result line the harness reports as $0.00 exactly must still be
// recorded as $0.00 (a real value), never coerced into the same nil that
// means "not reported" -- the other half of the same discipline.
func TestSubmit_GenuineZeroSpendIsNotConfusedWithAbsence(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(4242)
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	proc.lines <- resultLine(testSID, "OK", floatp(0.0))
	res, err := w.Submit(context.Background(), "a task")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Spend.CostUSD == nil {
		t.Fatalf("Result.Spend.CostUSD = nil, want a real pointer to 0.0")
	}
	if *res.Spend.CostUSD != 0.0 {
		t.Errorf("Result.Spend.CostUSD = %v, want 0.0", *res.Spend.CostUSD)
	}
}

// N5 (busy): this slice serialises Submit by construction -- a second
// concurrent Submit must be refused, not queued or silently dropped.
func TestSubmit_RefusesConcurrentSubmit(t *testing.T) {
	l := testLedger(t)
	proc := newFakeProcess(4242) // no line queued -- first Submit blocks
	clock := newFakeClock()
	w := startTestWorker(t, proc, l, clock, testSID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Submit(ctx, "a task that never gets an answer")

	deadline := time.Now().Add(time.Second)
	for proc.sentCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if proc.sentCount() == 0 {
		t.Fatalf("first Submit never reached the child")
	}

	if _, err := w.Submit(context.Background(), "a second task"); !errors.Is(err, ErrBusy) {
		t.Errorf("second concurrent Submit error = %v, want ErrBusy", err)
	}
}

// Start failing to spawn the child must still leave a terminal record
// behind for the lane id it minted -- never a dangling attempt nobody can
// account for.
func TestStart_SpawnFailureRecordsFailed(t *testing.T) {
	l := testLedger(t)
	cfg := Config{
		Issue:  "9999",
		Model:  "claude-sonnet-5",
		Dir:    t.TempDir(),
		Ledger: l,
		Mirror: mirror.Config{Enabled: false},
		Spawn: func(ctx context.Context, dir, model, sessionID string) (Process, error) {
			return nil, errors.New("no such binary")
		},
		NewSessionID: func() string { return testSID },
	}
	if _, err := Start(context.Background(), cfg); err == nil {
		t.Fatalf("Start did not report the spawn failure")
	}
	rec, ok := recordByID(t, l, testSID)
	if !ok {
		t.Fatalf("no ledger record for a lane id that was minted before spawning failed")
	}
	if rec.State != ledger.Failed {
		t.Errorf("state = %s, want Failed", rec.State)
	}
}

func TestStart_RequiresAModel(t *testing.T) {
	l := testLedger(t)
	_, err := Start(context.Background(), Config{Ledger: l})
	if err == nil {
		t.Fatalf("Start accepted a config with no model -- a worker must never inherit an ambient one")
	}
}

func TestStart_RequiresALedger(t *testing.T) {
	_, err := Start(context.Background(), Config{Model: "claude-sonnet-5"})
	if err == nil {
		t.Fatalf("Start accepted a config with no ledger")
	}
}

// --- small pure helpers ------------------------------------------------

func TestUserMessageLine_IsTheDocumentedShape(t *testing.T) {
	line, err := userMessageLine("hello")
	if err != nil {
		t.Fatalf("userMessageLine: %v", err)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("userMessageLine does not end in a newline, required for a JSONL child stdin")
	}
	for _, want := range []string{`"type":"user"`, `"role":"user"`, `"text":"hello"`} {
		if !strings.Contains(line, want) {
			t.Errorf("userMessageLine %q missing %q", line, want)
		}
	}
}

func TestIsResultLine(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`{"type":"result","result":"OK"}`, true},
		{`{"type":"system","subtype":"init"}`, false},
		{`{"type":"assistant","message":{}}`, false},
		{`not json at all`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := isResultLine(c.line); got != c.want {
			t.Errorf("isResultLine(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUID_IsAValidV4UUID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newUUID()
		if !uuidPattern.MatchString(id) {
			t.Fatalf("newUUID() = %q, does not match the RFC 4122 v4 shape claude --session-id requires", id)
		}
		if seen[id] {
			t.Fatalf("newUUID() produced a duplicate: %q", id)
		}
		seen[id] = true
	}
}
