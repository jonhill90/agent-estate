// Package mirror makes a dispatched turn watchable in tmux without making
// tmux the transport.
//
// WHY. 208 dispatches have run and the operator could not watch one of them.
// `estate dispatch` runs the harness as a subprocess and reads its stdout;
// nothing surfaces that stream anywhere a human can look while it runs. Two
// of the operator's hard parameters say that is the wrong shape:
// `terminals=tmux_persistent_required` and
// `observability=jon_can_watch_via_tmux` -- whatever the transport, he must
// be able to watch it.
//
// TRANSPORT AND VISIBILITY ARE SEPARATE, AND THIS PACKAGE IS ONLY THE SECOND.
// The turn is still a subprocess whose stdout the caller owns. What this adds
// is a MIRROR of that stream: the same bytes, appended to a transcript file,
// with a tmux window opened to follow that file. Nothing here ever sends keys
// into a pane; there is no code path in this package that writes INTO the
// window it opens, only one that opens a reader of a file. A control path is
// the thing the estate is moving away from, not the thing this adds.
//
// THE FILE IS THE MIRROR; THE PANE IS A VIEWER OF THE FILE. That indirection
// is what makes both failure directions safe, structurally rather than by
// care:
//
//   - A pane dying cannot kill the turn. The pane runs `tail -f` on the
//     transcript. It is not the turn's stdout, not its stdin, and not its
//     parent; killing it kills a `tail`. Writes into the transcript also
//     never propagate an error back to the caller (see sink.Write), so not
//     even a full disk can take a turn down through this package.
//   - A turn dying leaves its pane readable. The transcript is a file on
//     disk, not a pane's scrollback, and the viewer keeps following it after
//     the writer is gone. Close() stamps a footer naming the terminal state,
//     so the pane a human opens after an API error, a quota window or a
//     signal kill says what happened rather than vanishing.
//
// BOUNDED, AND SAID OUT LOUD. A pane per CONCURRENT turn is bounded by the
// in-flight cap the caller passes (6 today, pressure.Default().MaxInFlight).
// A pane per HISTORICAL turn is not -- 208 panes is the same unbounded-growth
// defect as the 176 worktrees that OOM-killed this host. Open() therefore
// retires ended mirror windows before opening a new one, and REFUSES to open
// one at all (ErrAtCapacity, dispatch continues unmirrored) when the bound is
// full of live turns. Transcripts on disk are bounded the same way, by
// Config.Keep.
//
// LIVENESS IS DECIDED WITHOUT THE DYING PROCESS'S COOPERATION. What makes a
// window retireable is ended(), and it does NOT simply look for the footer
// Close() writes: a footer is only ever produced by a graceful exit, and the
// deaths this estate has are SIGKILL and the OOM killer. A bound whose
// liveness signal requires cooperation degrades into a permanent refusal
// after enough hard deaths -- see ended's own comment for the three
// cooperation-free signals that replaced it.
//
// TMUX SAFETY. A bare `tmux kill-server` from a lane destroyed this estate
// three times in one day, and a loop over window INDEXES once destroyed the
// Telegram poller, because killing window 4 renumbers 5 into 4. So:
//
//   - Every tmux invocation goes through tmuxCmd, which refuses any verb
//     outside a six-entry allowlist (see allowedVerbs). That is the
//     enforcement: a control verb does not run here regardless of which file
//     asked for it or how the string was assembled.
//   - Nothing here runs kill-server or kill-session, ever. The only
//     destructive verb in this package is kill-window.
//   - kill-window is only ever given a `#{window_id}` (`@7`) read out of
//     list-windows for OUR configured session in the same call, never an
//     index and never a name.
//   - Every -t target is `=`-prefixed, so tmux exact-matches it instead of
//     prefix-matching some other session that happens to start with the
//     same letters.
//   - Config.Protected refuses named sessions outright (the operator's own
//     session is protected), and the window holding this process's own pane
//     is never a reap candidate.
//   - Config.TmuxTmpdir scopes every tmux invocation to a private socket
//     directory with $TMUX unset -- the isolation idiom the tests use so no
//     test in this repo can address the default socket.
//
// Note the apparent tension with the operator parameter
// `lane_addressing=session_index_not_raw_window_id`: that rule is about
// DELIVERING to a lane across a tmux server restart, where a stale window id
// sends silently into nothing. This package never delivers anything, and only
// ever uses a window id inside the same list-windows/kill-window pair that
// produced it, which is the case invariant 5 exists for.
package mirror

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// WindowPrefix names every window this package creates. It is also the only
// thing that makes a window a reap candidate: a window without this prefix is
// somebody else's and is never touched, whatever session it is in.
const WindowPrefix = "estate-turn-"

// footerMarker is what Close() stamps at the end of a transcript. It is the
// FIRST of three signals ended() uses, not the only one -- see ended's own
// comment for why a footer alone is not a liveness test.
//
// It is deliberately a property of the transcript, not a second registry
// file: the transcript is the record, and a registry that could disagree with
// it would be a new class of drift.
const footerMarker = "=== estate: turn ended"

// ownerMarker prefixes the header line naming the process that is writing a
// transcript. It exists because a footer is only ever written by a GRACEFUL
// exit, and the deaths this estate actually suffers are not graceful -- this
// host was OOM-killed the day this package was written, and SIGKILL skips
// every defer. A pid is a liveness signal the dying process does not have to
// cooperate to produce.
const ownerMarker = "owner-pid: "

var (
	// ErrDisabled means the caller switched mirroring off. Not a failure.
	ErrDisabled = errors.New("mirror: disabled")
	// ErrAtCapacity means every mirror window in the bound belongs to a turn
	// that is still running, so no new one may be opened. The caller
	// dispatches anyway, unmirrored -- visibility is never allowed to gate
	// work.
	ErrAtCapacity = errors.New("mirror: every window in the bound belongs to a live turn")
)

// Config is how a caller sizes and scopes mirroring.
type Config struct {
	// Enabled false makes Open return ErrDisabled and do nothing else.
	Enabled bool
	// Session is the tmux session mirror windows are opened in. Created if
	// absent. Exact-matched, never prefix-matched.
	Session string
	// Dir holds the transcripts. One file per turn, named for its dispatch id.
	Dir string
	// Max bounds how many mirror windows may exist in Session at once. The
	// caller should pass the same in-flight cap that bounds concurrent turns,
	// so the two cannot drift apart.
	Max int
	// Keep bounds how many transcripts stay on disk. Only ended transcripts
	// with no live window are ever removed.
	Keep int
	// Protected names sessions this package refuses to touch at all, whatever
	// else it is asked to do.
	Protected []string
	// TmuxTmpdir, when set, scopes every tmux invocation to that socket
	// directory with $TMUX unset. Tests MUST set this; production leaves it
	// empty and uses the caller's own tmux server.
	TmuxTmpdir string
	// Heartbeat is how often an "elapsed" line is written into the transcript
	// while the turn runs. It exists because a harness may print nothing at
	// all until it finishes (claude -p --output-format json emits one JSON
	// envelope at the end), and a pane that has shown nothing for ten minutes
	// is indistinguishable from a pane that is broken. Zero disables it.
	Heartbeat time.Duration
	// MaxAge is the backstop half of the liveness test: a transcript with no
	// footer that has not been written to for longer than this belongs to a
	// turn that cannot still be running, whatever a pid says. It must stay
	// comfortably LONGER than the caller's own turn timeout (45m in
	// `estate dispatch`), because the only cost of being generous here is a
	// dead pane surviving a while, while the cost of being mean is retiring a
	// live turn's screen.
	MaxAge time.Duration
	// OwnerPID is the process whose death means this transcript can never gain
	// a footer. Zero means this process. Injectable so a test can write a
	// transcript owned by a pid it controls the liveness of.
	OwnerPID int
	// Alive answers "is that pid still running". Nil means the real probe.
	// A probe that cannot tell must answer true: never retire on ignorance.
	Alive func(pid int) bool
	// Now is the clock, injectable for tests.
	Now func() time.Time
}

// Default is mirroring as dispatch uses it. max should be the caller's
// in-flight cap.
func Default(max int) Config {
	return Config{
		Enabled:   true,
		Session:   "estate",
		Dir:       defaultDir(),
		Max:       max,
		Keep:      100,
		Protected: []string{"Hill90"},
		Heartbeat: 15 * time.Second,
		// Twice the caller's 45m turn timeout. A turn that reached the timeout
		// is killed by its own context, so a transcript untouched for 90
		// minutes cannot belong to anything still running.
		MaxAge: 90 * time.Minute,
	}
}

func defaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "estate-mirror")
	}
	return filepath.Join(home, ".local", "state", "estate", "mirror")
}

func (c Config) withDefaults() Config {
	if c.Session == "" {
		c.Session = "estate"
	}
	if c.Dir == "" {
		c.Dir = defaultDir()
	}
	if c.Max <= 0 {
		c.Max = 6
	}
	if c.Keep <= 0 {
		c.Keep = 100
	}
	if c.MaxAge <= 0 {
		c.MaxAge = 90 * time.Minute
	}
	if c.OwnerPID == 0 {
		c.OwnerPID = os.Getpid()
	}
	if c.Alive == nil {
		c.Alive = pidAlive
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// pidAlive is the real liveness probe. Signal 0 delivers nothing; it only
// asks the kernel whether the pid can be signalled.
//
// EVERY UNCERTAIN ANSWER IS "ALIVE". A permission error means the process
// exists and belongs to someone else; a lookup that fails for any other
// reason means we could not tell. Both return true, because the cost of a
// wrong "dead" is retiring a running turn's screen and the cost of a wrong
// "alive" is a dead pane lingering until MaxAge catches it.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	err = p.Signal(syscall.Signal(0))
	switch {
	case err == nil:
		return true
	case errors.Is(err, syscall.EPERM), errors.Is(err, os.ErrPermission):
		return true
	case errors.Is(err, syscall.ESRCH), errors.Is(err, os.ErrProcessDone):
		return false
	default:
		// An answer nobody recognises is not evidence of death.
		return true
	}
}

// Meta is what the header of a transcript states about the turn, all of it
// known to the estate at dispatch time.
type Meta struct {
	ID       string
	Issue    string
	Role     string
	Harness  string
	Worktree string
}

// Mirror is one turn's transcript, plus the tmux window following it if one
// could be opened. Every method is safe on a nil receiver, so a caller that
// could not open a mirror keeps exactly one branch (`m, _ := Open(...)`) and
// never a nil check per call site.
type Mirror struct {
	cfg      Config
	meta     Meta
	path     string
	windowID string
	note     string
	started  time.Time

	s      *sink
	stop   chan struct{}
	closed sync.Once
}

// Open creates the turn's transcript and, if it can, a tmux window following
// it.
//
// ORDER MATTERS AND IS NOT INCIDENTAL: the durable thing (the transcript) is
// created and written before the visible thing (the window). A crash between
// the two leaves a readable transcript with no pane, which is a report; the
// reverse order would leave a pane following a file that does not exist,
// which is a screen that lies. Same discipline as lane-done.sh's
// release-then-rename.
//
// A tmux failure is therefore NOT an error: the returned Mirror is fully
// usable, Note() says why there is no pane, and that reason is also written
// into the transcript itself so the record explains its own gap. Open only
// errors when the transcript cannot be created at all (or mirroring is off,
// or the bound is full of live turns) -- and even then the caller must
// dispatch anyway.
func Open(cfg Config, m Meta) (*Mirror, error) {
	if !cfg.Enabled {
		return nil, ErrDisabled
	}
	cfg = cfg.withDefaults()
	if err := cfg.checkSession(); err != nil {
		return nil, err
	}
	// Refused, not degraded: an unusable socket directory means tmux would
	// quietly use the operator's own server instead of the private one the
	// caller asked for.
	if err := cfg.checkSocketDir(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(m.ID) == "" {
		return nil, errors.New("mirror: refusing to open a transcript for a turn with no dispatch id")
	}
	if err := safeID(m.ID); err != nil {
		return nil, err
	}

	// Capacity is judged BEFORE the transcript is created, so a refusal
	// leaves nothing behind. reap() may free room by retiring ended windows.
	if err := reap(cfg); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("mirror: cannot create %s: %w", cfg.Dir, err)
	}
	path := filepath.Join(cfg.Dir, m.ID+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mirror: cannot open transcript %s: %w", path, err)
	}

	mi := &Mirror{
		cfg:     cfg,
		meta:    m,
		path:    path,
		started: cfg.Now(),
		s:       &sink{f: f},
		stop:    make(chan struct{}),
	}
	mi.writeHeader()

	// Only now the visible half.
	id, werr := openWindow(cfg, m.ID, path)
	if werr != nil {
		mi.note = werr.Error()
		mi.Logf("no tmux window for this turn: %v", werr)
		mi.Logf("the turn is running and this transcript is live -- read it with: tail -f %s", path)
	} else {
		mi.windowID = id
		mi.Logf("mirrored in tmux session %s, window %s (a viewer; nothing typed there reaches the agent)", cfg.Session, id)
	}

	prune(cfg)
	mi.startHeartbeat()
	return mi, nil
}

// checkSession refuses a protected session outright. This runs before
// anything else touches tmux so a misconfiguration cannot get as far as
// listing, let alone killing, a window in the operator's own session.
func (c Config) checkSession() error {
	for _, p := range c.Protected {
		if strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(c.Session)) {
			return fmt.Errorf("mirror: refusing to open windows in protected session %q", c.Session)
		}
	}
	if strings.ContainsAny(c.Session, ":. \t") || strings.TrimSpace(c.Session) == "" {
		return fmt.Errorf("mirror: %q is not a usable tmux session name", c.Session)
	}
	return nil
}

// safeID refuses a dispatch id that could name a file outside Dir. The id
// already comes from internal/dispatchid, which mints safe ids; this is here
// so the refusal is structural rather than a trusted upstream.
func safeID(id string) error {
	if id != filepath.Clean(id) || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("mirror: dispatch id %q is not a single safe path element", id)
	}
	if strings.Trim(id, ".") == "" {
		return fmt.Errorf("mirror: dispatch id %q names a directory rather than a transcript", id)
	}
	return nil
}

func (m *Mirror) writeHeader() {
	var b strings.Builder
	fmt.Fprintf(&b, "=== estate turn %s ===\n", m.meta.ID)
	fmt.Fprintf(&b, "issue:    %s\n", m.meta.Issue)
	fmt.Fprintf(&b, "role:     %s\n", m.meta.Role)
	fmt.Fprintf(&b, "harness:  %s\n", m.meta.Harness)
	fmt.Fprintf(&b, "worktree: %s\n", m.meta.Worktree)
	fmt.Fprintf(&b, "started:  %s\n", m.started.Format(time.RFC3339))
	// The liveness signal, written at the top where it cannot depend on the
	// dying process cooperating later. See ended().
	fmt.Fprintf(&b, "%s%d\n", ownerMarker, m.cfg.OwnerPID)
	b.WriteString("\nThis pane is a MIRROR of the turn's output, not a terminal it runs in.\n")
	b.WriteString("The estate never types into it and nothing you type here reaches the agent.\n")
	b.WriteString("Killing this pane does not kill the turn; the turn is a subprocess elsewhere.\n")
	b.WriteString("---\n")
	m.s.Write([]byte(b.String()))
}

// Stdout returns the writer a caller should tee the turn's stdout into.
// Bytes arrive in the transcript as the child writes them, so the pane shows
// a streaming harness live. A harness that buffers its own output until exit
// (claude -p --output-format json) will show nothing here until it exits;
// that is the harness's shape, not this package's, and the heartbeat exists
// so the pane is still visibly alive meanwhile.
func (m *Mirror) Stdout() io.Writer {
	if m == nil {
		return io.Discard
	}
	return m.s
}

// Stderr returns the writer for the turn's stderr, line-tagged so an error
// is distinguishable from output in the same transcript. The three
// infrastructure deaths this estate actually sees -- API error, quota
// window, signal kill -- announce themselves here.
func (m *Mirror) Stderr() io.Writer {
	if m == nil {
		return io.Discard
	}
	return &lineWriter{out: m.s, prefix: "[stderr] "}
}

// Logf writes one estate-authored line into the transcript. Every such line
// is tagged [estate] so a reader can always tell what the agent produced from
// what the supervisor said about it.
func (m *Mirror) Logf(format string, a ...any) {
	if m == nil {
		return
	}
	m.s.Write([]byte("[estate] " + fmt.Sprintf(format, a...) + "\n"))
}

// Path is the transcript's path, "" if there is no mirror.
func (m *Mirror) Path() string {
	if m == nil {
		return ""
	}
	return m.path
}

// WindowID is the tmux window following this transcript, "" if none was
// opened. Absence is a real state here, not a failure to look.
func (m *Mirror) WindowID() string {
	if m == nil {
		return ""
	}
	return m.windowID
}

// Note explains why there is no window, "" when there is one (or when there
// is no mirror at all).
func (m *Mirror) Note() string {
	if m == nil {
		return ""
	}
	return m.note
}

// Close stamps the terminal state into the transcript and stops the
// heartbeat. It deliberately does NOT kill the window: a turn dying is
// exactly when a human wants the screen, so the pane stays, holding the
// footer, until a later dispatch retires it under the bound.
func (m *Mirror) Close(state, note string) {
	if m == nil {
		return
	}
	m.closed.Do(func() {
		close(m.stop)
		var b strings.Builder
		b.WriteString("---\n")
		fmt.Fprintf(&b, "%s %s at %s (after %s)\n", footerMarker, state,
			m.cfg.Now().Format(time.RFC3339), m.cfg.Now().Sub(m.started).Round(time.Second))
		if strings.TrimSpace(note) != "" {
			fmt.Fprintf(&b, "%s\n", strings.TrimSpace(note))
		}
		b.WriteString("this pane is now a record; it stays readable until a later dispatch retires it\n")
		m.s.Write([]byte(b.String()))
		m.s.close()
	})
}

func (m *Mirror) startHeartbeat() {
	if m.cfg.Heartbeat <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(m.cfg.Heartbeat)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				m.Logf("still running, %s elapsed", m.cfg.Now().Sub(m.started).Round(time.Second))
			}
		}
	}()
}

// --- the transcript sink -------------------------------------------------

// sink is the one writer into a transcript. It serialises the several
// goroutines that write concurrently (os/exec's stdout copier, its stderr
// copier, the heartbeat, the caller) and -- the part that matters -- it NEVER
// returns an error.
//
// That is not sloppiness. cmd.Stdout is copied by os/exec in a goroutine that
// aborts the copy on a write error and can leave the child taking SIGPIPE. If
// this writer could fail, a full disk or a revoked permission on the
// transcript would kill a running turn, which is precisely the "the mirror
// must not become a new way to lose work" requirement. So a broken transcript
// degrades to no transcript, silently, and the turn runs on.
type sink struct {
	mu     sync.Mutex
	f      *os.File
	broken bool
}

func (s *sink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil && !s.broken {
		if _, err := s.f.Write(p); err != nil {
			s.broken = true
		}
	}
	return len(p), nil
}

func (s *sink) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil {
		s.f.Close()
		s.f = nil
	}
}

// lineWriter prefixes each complete line it forwards. A trailing partial line
// is held until its newline arrives, so a prefix never lands mid-line; on the
// stderr path the child's exit closes the pipe and anything still held is
// flushed by Flush -- which the caller does not need to call, because Close's
// footer is what a reader looks for and a held partial line is at most the
// last unterminated fragment of stderr.
type lineWriter struct {
	mu     sync.Mutex
	out    io.Writer
	prefix string
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := indexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i+1]
		w.out.Write(append([]byte(w.prefix), line...))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// --- tmux ----------------------------------------------------------------

// checkSocketDir refuses a socket directory that does not exist.
//
// THIS IS NOT PEDANTRY, it is the exact way invariant 4 gets broken while
// looking obeyed. tmux given a $TMUX_TMPDIR that is not a usable directory
// does not fail -- it SILENTLY falls back to the default socket. So a caller
// that believed it was isolated (a test, a demonstration) creates its session
// on the operator's real server, beside Hill90, and nothing says a word. That
// happened once while this package was being built, and the only reason it
// was caught was a stray `ls`.
//
// Refusing here converts a silent misdirection into a loud refusal. Absence
// of isolation must never present as isolation.
func (c Config) checkSocketDir() error {
	if c.TmuxTmpdir == "" {
		return nil
	}
	fi, err := os.Stat(c.TmuxTmpdir)
	if err != nil {
		return fmt.Errorf("mirror: socket directory %s is unusable (%v) -- tmux would silently fall back to the DEFAULT socket, which is the operator's own server", c.TmuxTmpdir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("mirror: socket directory %s is not a directory -- tmux would silently fall back to the DEFAULT socket, which is the operator's own server", c.TmuxTmpdir)
	}
	return nil
}

// allowedVerbs is every tmux verb this package may run, and the whole of it.
//
// THIS IS THE MECHANISM, NOT THE SOURCE SCANS. "This package is a viewer,
// never a control path" was previously guarded only by reading source for
// banned strings, which a sibling file in the same package or a verb built
// from concatenated literals walks straight past (both demonstrated in review
// of #1003). An allowlist checked where the command is actually built cannot
// be evaded by how the verb was spelled or which file spelled it: a verb that
// is not on this list does not run, however it was computed.
//
// Note what is on it: five read/create verbs, and exactly one destructive
// one. kill-window is here because the bound needs it; kill-server,
// kill-session and kill-pane are absent, and so is every verb that writes
// INTO a pane (send-keys, paste-buffer, respawn-*, run-shell).
var allowedVerbs = map[string]bool{
	"has-session":     true,
	"new-session":     true,
	"list-windows":    true,
	"display-message": true,
	"new-window":      true,
	"kill-window":     true,
}

// tmuxCmd builds a tmux invocation scoped to cfg's socket directory, and
// refuses any verb outside allowedVerbs. When TmuxTmpdir is set the command
// runs against a private server with $TMUX unset -- the isolation idiom every
// test in this repo must use, so no test can address the default socket.
// Callers must have passed checkSocketDir first; see its comment for what an
// unusable directory silently does.
//
// A refused verb is returned as a command that cannot start (exec.Cmd.Err is
// returned by Start, so Run/Output/CombinedOutput all surface it) rather than
// as a panic or an os.Exit: this package must never be able to take a running
// turn down, and every caller here already treats a failed tmux call as "no
// pane", which is the correct outcome for a verb that should not exist.
func tmuxCmd(cfg Config, args ...string) *exec.Cmd {
	if len(args) == 0 || !allowedVerbs[args[0]] {
		verb := "<none>"
		if len(args) > 0 {
			verb = args[0]
		}
		c := exec.Command("tmux")
		c.Err = fmt.Errorf("mirror: refusing tmux verb %q -- this package is a viewer; only %s may run", verb, strings.Join(sortedVerbs(), ", "))
		return c
	}
	c := exec.Command("tmux", args...)
	if cfg.TmuxTmpdir != "" {
		env := make([]string, 0, len(os.Environ())+1)
		for _, e := range os.Environ() {
			if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_TMPDIR=") {
				continue
			}
			env = append(env, e)
		}
		c.Env = append(env, "TMUX_TMPDIR="+cfg.TmuxTmpdir)
	}
	return c
}

func sortedVerbs() []string {
	vs := make([]string, 0, len(allowedVerbs))
	for v := range allowedVerbs {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	return vs
}

// target exact-matches a session. tmux prefix-matches a bare name, so
// `-t estate` can resolve to `estate-scratch`; `=estate` cannot.
func target(session string) string { return "=" + session }

func ensureSession(cfg Config) error {
	if err := tmuxCmd(cfg, "has-session", "-t", target(cfg.Session)).Run(); err == nil {
		return nil
	}
	// new-session -d creates a detached session; it does not attach, does not
	// touch any other session, and is the only creating verb here.
	out, err := tmuxCmd(cfg, "new-session", "-d", "-s", cfg.Session).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot create tmux session %s: %v: %s", cfg.Session, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// win is one mirror window as tmux reports it: an id we may address and the
// dispatch id its name carries.
type win struct {
	ID         string
	Name       string
	DispatchID string
}

// windows lists the mirror windows in cfg.Session. Only windows carrying
// WindowPrefix are returned -- anything else in the session belongs to
// somebody else and this package must not even consider it.
func windows(cfg Config) ([]win, error) {
	out, err := tmuxCmd(cfg, "list-windows", "-t", target(cfg.Session), "-F", "#{window_id}\t#{window_name}").Output()
	if err != nil {
		return nil, fmt.Errorf("cannot list windows in session %s: %w", cfg.Session, err)
	}
	var ws []win
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, name, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(name, WindowPrefix) {
			continue
		}
		ws = append(ws, win{ID: id, Name: name, DispatchID: strings.TrimPrefix(name, WindowPrefix)})
	}
	return ws, nil
}

// ended reports whether a turn is over, and therefore whether its window may
// be retired and its transcript pruned.
//
// A FOOTER IS NOT A LIVENESS TEST, and treating it as one was a real defect
// (found in review of #1003). Close() writes the footer, and Close() only
// runs on a graceful exit. The deaths this estate actually has are SIGKILL
// and the OOM killer, which skip every defer -- so "no footer" meant "live
// forever", one poisoned window per hard death, until six of them filled the
// bound and every later dispatch ran unmirrored with no cure but a human
// removing a window by hand. That is the 176-worktree shape: nothing cleans
// up at a non-graceful death.
//
// So liveness is decided by three signals, none of which needs the dying
// process to have written anything:
//
//  1. The transcript cannot be read. Ended -- a window following a file
//     nobody can read is not a mirror of anything live.
//  2. The footer is there. Ended, and this stays the fast, exact answer for
//     the overwhelmingly common graceful case.
//  3. The owning process is gone. Ended -- a transcript's footer can only
//     ever be written by the process named in its own header, so once that
//     pid is not running, no footer is coming. An UNCERTAIN probe answers
//     "alive" (see pidAlive), so this only fires on a positive death.
//  4. Nothing has written to it for MaxAge. Ended -- the backstop for a
//     transcript with no owner line (written by an older build) and for pid
//     reuse, where a recycled pid makes a dead turn look live. A running turn
//     writes a heartbeat every 15s and is killed by its own 45m timeout, so
//     90 minutes of silence cannot be a live turn.
//
// The failure directions are deliberately asymmetric. Wrongly "ended"
// retires a screen someone might be watching; wrongly "live" leaves a dead
// pane up a little longer. Every uncertainty above resolves toward "live",
// and MaxAge is the only thing that ever overrides that -- which is why it is
// set at twice the turn timeout rather than near it.
func ended(cfg Config, dispatchID string) bool {
	// Idempotent, and it is what makes a zero-valued MaxAge mean "the
	// default" rather than "everything is stale".
	cfg = cfg.withDefaults()
	path := filepath.Join(cfg.Dir, dispatchID+".log")
	b, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	if strings.Contains(string(b), footerMarker) {
		return true
	}
	if pid, ok := ownerPID(string(b)); ok && cfg.Alive != nil && !cfg.Alive(pid) {
		return true
	}
	fi, serr := os.Stat(path)
	if serr != nil {
		return true
	}
	return cfg.Now().Sub(fi.ModTime()) > cfg.MaxAge
}

// ownerPID reads the pid the header names. Absent or unparseable is reported
// as "no answer" rather than as pid 0, so a caller falls through to the age
// backstop instead of treating a missing line as a dead process.
func ownerPID(transcript string) (int, bool) {
	i := strings.Index(transcript, ownerMarker)
	if i < 0 {
		return 0, false
	}
	rest := transcript[i+len(ownerMarker):]
	if j := strings.IndexAny(rest, "\r\n"); j >= 0 {
		rest = rest[:j]
	}
	pid, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// ownWindow is the window holding this process's own pane, if it has one. It
// is never a reap candidate: retiring the window the supervisor is running in
// would be this package killing its own caller.
func ownWindow(cfg Config) string {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return ""
	}
	out, err := tmuxCmd(cfg, "display-message", "-p", "-t", pane, "#{window_id}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// reap enforces the window bound. It retires ENDED mirror windows, oldest
// transcript first, until there is room for one more. If the bound is full of
// windows whose turns are still running it returns ErrAtCapacity rather than
// killing a live one -- a live turn's mirror is the whole point, and a
// visibility feature must never be the thing that decides what work gets
// watched by force.
//
// Every id handed to kill-window came out of the list-windows call above, for
// our own session, carrying our own prefix. No index is ever addressed.
func reap(cfg Config) error {
	if err := ensureSession(cfg); err != nil {
		// No tmux is not a dispatch failure. The transcript still gets made.
		return nil
	}
	ws, err := windows(cfg)
	if err != nil {
		return nil
	}
	if len(ws) < cfg.Max {
		return nil
	}
	self := ownWindow(cfg)
	var dead []win
	for _, w := range ws {
		if w.ID == self {
			continue
		}
		if ended(cfg, w.DispatchID) {
			dead = append(dead, w)
		}
	}
	// Oldest first, by the transcript's own last-write time: the least
	// recently active record is the one a human is least likely to still want.
	sort.Slice(dead, func(i, j int) bool {
		return transcriptTime(cfg, dead[i].DispatchID).Before(transcriptTime(cfg, dead[j].DispatchID))
	})
	need := len(ws) - cfg.Max + 1
	for i := 0; i < need; i++ {
		if i >= len(dead) {
			return fmt.Errorf("%w: %d windows, bound is %d", ErrAtCapacity, len(ws), cfg.Max)
		}
		tmuxCmd(cfg, "kill-window", "-t", dead[i].ID).Run()
	}
	return nil
}

func transcriptTime(cfg Config, dispatchID string) time.Time {
	fi, err := os.Stat(filepath.Join(cfg.Dir, dispatchID+".log"))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// prune bounds transcripts on disk. Only an ended transcript with no live
// mirror window is ever removed -- deleting one a pane is still following
// would blank a screen somebody is reading, and deleting a running turn's
// would throw away the only live record of it.
func prune(cfg Config) {
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return
	}
	live := map[string]bool{}
	if ws, werr := windows(cfg); werr == nil {
		for _, w := range ws {
			live[w.DispatchID] = true
		}
	}
	type cand struct {
		path string
		mod  time.Time
	}
	var cs []cand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".log")
		if live[id] {
			continue
		}
		if !ended(cfg, id) {
			continue
		}
		fi, ferr := e.Info()
		if ferr != nil {
			continue
		}
		cs = append(cs, cand{filepath.Join(cfg.Dir, e.Name()), fi.ModTime()})
	}
	if len(cs) <= cfg.Keep {
		return
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].mod.Before(cs[j].mod) })
	for i := 0; i < len(cs)-cfg.Keep; i++ {
		os.Remove(cs[i].path)
	}
}

// openWindow opens a detached window running a follower of path.
//
// The window runs `tail -n +1 -f` and NOTHING ELSE: it reads a file. It has
// no handle on the turn's process, no pipe into its stdin, and no way to
// affect it. That is the structural guarantee behind "a pane dying must not
// kill the turn" -- there is nothing for the pane to kill.
//
// -d keeps the operator's current window where it is; opening a mirror must
// never yank somebody's screen to a new pane mid-task.
func openWindow(cfg Config, dispatchID, path string) (string, error) {
	if err := ensureSession(cfg); err != nil {
		return "", err
	}
	out, err := tmuxCmd(cfg,
		"new-window", "-d",
		"-t", target(cfg.Session)+":",
		"-n", WindowPrefix+dispatchID,
		"-P", "-F", "#{window_id}",
		"tail", "-n", "+1", "-f", path,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cannot open mirror window: %v: %s", err, strings.TrimSpace(string(out)))
	}
	id := strings.TrimSpace(string(out))
	if !strings.HasPrefix(id, "@") {
		return "", fmt.Errorf("tmux did not report a window id, got %q", id)
	}
	return id, nil
}
