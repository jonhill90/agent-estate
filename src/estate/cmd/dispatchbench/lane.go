package main

// A PERSISTENT LANE: one interactive `claude` living in a tmux pane, fed one
// prompt after another, never restarted between turns. This is the arm the
// estate does not currently run and #1002 exists to price.
//
// TMUX SAFETY, which is invariant 4 and was learned by destroying the live
// estate three times in one day. Every call here goes through tmuxCmd, which
// refuses unless TMUX_TMPDIR names a private directory under the system temp
// dir, unsets $TMUX so an inherited client cannot be addressed by accident,
// and passes an explicit -L socket name. The operator's own server is
// unreachable from this file by construction, not by care.
//
// kill-server IS in the allowlist here, unlike internal/mirror's, and the
// difference is the isolation assertion above it: mirror addresses the
// operator's own server, where kill-server is unthinkable; this file can only
// ever address a socket it created inside a directory it created.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var laneAllowedVerbs = map[string]bool{
	"new-session":  true,
	"has-session":  true,
	"list-panes":   true,
	"capture-pane": true,
	"send-keys":    true,
	"kill-server":  true,
}

type lane struct {
	tmpdir  string
	socket  string
	session string
	dir     string
	panePid int
}

func (l *lane) tmuxCmd(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if len(args) == 0 || !laneAllowedVerbs[args[0]] {
		return nil, fmt.Errorf("tmux verb %q is not in this package's allowlist", strings.Join(args[:1], ""))
	}
	if err := assertIsolated(l.tmpdir); err != nil {
		return nil, err
	}
	full := append([]string{"-L", l.socket}, args...)
	cmd := exec.CommandContext(ctx, "tmux", full...)
	env := os.Environ()
	out := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_TMPDIR=") {
			continue
		}
		out = append(out, kv)
	}
	cmd.Env = append(out, "TMUX_TMPDIR="+l.tmpdir)
	return cmd, nil
}

// assertIsolated is the check that makes every verb above safe. An empty or
// non-private TMUX_TMPDIR means the next command would reach the default
// socket -- the operator's own sessions and the estate's live one.
func assertIsolated(tmpdir string) error {
	if tmpdir == "" {
		return fmt.Errorf("refusing to run tmux with no TMUX_TMPDIR: that addresses the default socket")
	}
	ok := false
	for _, root := range tmuxTmpdirRoots() {
		if strings.HasPrefix(tmpdir, strings.TrimSuffix(root, "/")+"/") {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("refusing to run tmux: TMUX_TMPDIR %q is not a private directory under one of %v", tmpdir, tmuxTmpdirRoots())
	}
	st, err := os.Stat(tmpdir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("refusing to run tmux: TMUX_TMPDIR %q is not a directory", tmpdir)
	}
	return nil
}

// tmuxTmpdirRoots are the directories a benchmark lane's private socket may
// live under.
//
// "/tmp" IS IN THE LIST FOR A LENGTH REASON, not a taste one. A unix socket
// path is capped near 104 bytes, and macOS's per-user $TMPDIR
// (/var/folders/_b/n12.../T/) spends 49 of them before tmux appends
// "<dir>/tmux-501/<socket>" -- the first run of this program failed with
// "File name too long" and no lane at all. A short root is what makes an
// isolated socket possible here; without one the only working socket would be
// the default one, which invariant 4 forbids outright.
func tmuxTmpdirRoots() []string {
	roots := []string{os.TempDir()}
	if st, err := os.Stat("/tmp"); err == nil && st.IsDir() {
		roots = append(roots, "/tmp")
	}
	return roots
}

func (l *lane) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd, err := l.tmuxCmd(ctx, args...)
	if err != nil {
		return nil, err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// startLane brings up the pane and waits until the harness is actually ready
// to take a prompt. "Ready" is read off the pane, which is the only place
// Claude Code says so -- there is no transcript record for "the input box
// exists". Everything AFTER this point reads the transcript instead.
func startLane(ctx context.Context, dir, tmpdir, socket string, extraArgs []string) (*lane, error) {
	l := &lane{tmpdir: tmpdir, socket: socket, session: "bench", dir: dir}
	argv := append([]string{"claude", "--dangerously-skip-permissions"}, extraArgs...)
	if _, err := l.run(ctx, "new-session", "-d", "-s", l.session, "-x", "200", "-y", "50", "-c", dir, strings.Join(argv, " ")); err != nil {
		return nil, err
	}

	out, err := l.run(ctx, "list-panes", "-t", l.session, "-F", "#{pane_pid}")
	if err != nil {
		return nil, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(strings.Split(string(out), "\n")[0]))
	if err != nil {
		return nil, fmt.Errorf("could not read the lane's pane pid: %w", err)
	}
	l.panePid = pid

	deadline := time.Now().Add(90 * time.Second)
	trusted := false
	for {
		if time.Now().After(deadline) {
			pane, _ := l.Capture(ctx)
			return nil, fmt.Errorf("the lane never became ready to take a prompt; last pane:\n%s", pane)
		}
		pane, err := l.Capture(ctx)
		if err != nil {
			return nil, err
		}
		// The trust dialog appears for a directory Claude Code has not seen
		// before, which a fresh scratch dir always is. Answering it is the one
		// piece of pane-driven interaction here.
		if !trusted && strings.Contains(pane, "trust this folder") {
			if _, err := l.run(ctx, "send-keys", "-t", l.session, "Enter"); err != nil {
				return nil, err
			}
			trusted = true
			time.Sleep(2 * time.Second)
			continue
		}
		if strings.Contains(pane, "bypass permissions on") {
			return l, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (l *lane) Capture(ctx context.Context) (string, error) {
	out, err := l.run(ctx, "capture-pane", "-p", "-t", l.session)
	return string(out), err
}

// Send types a prompt and submits it. -l sends the text literally, so nothing
// in a prompt can be interpreted as a key name; the Enter is a separate call
// for the same reason.
func (l *lane) Send(ctx context.Context, prompt string) error {
	if strings.ContainsAny(prompt, "\n\r") {
		return fmt.Errorf("a lane prompt must be one line; this one contains a newline, which would submit it early")
	}
	if _, err := l.run(ctx, "send-keys", "-t", l.session, "-l", prompt); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond) // let the input box settle before submitting
	_, err := l.run(ctx, "send-keys", "-t", l.session, "Enter")
	return err
}

// Close tears down the whole private server. See the file header for why
// kill-server is defensible here and nowhere else in this repo.
func (l *lane) Close() error {
	if l == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := l.run(ctx, "kill-server")
	return err
}
