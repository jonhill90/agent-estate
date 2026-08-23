// Package sshserver serves an existing tea.Model over SSH using
// charmbracelet/wish (agent-tui#67) -- it never renders anything itself and
// never knows this is estate's shell.Model specifically; cmd/estate wires
// that in as a Handler. wish is the adopt-vs-build answer named by #67's own
// brief: it exists specifically to serve Bubble Tea apps over SSH, is in the
// same GitHub org as Bubble Tea (github.com/charmbracelet), MIT licensed,
// 5.4k stars, pushed within the last month as of 2026-08-20, and backs
// Charm's own soft-serve SSH git server in production. This package pins
// wish v1.4.7 (not the v2 module, charm.land/wish/v2) because v2 requires
// charm.land/bubbletea/v2 -- a different major version from the
// github.com/charmbracelet/bubbletea v1 line internal/shell and every other
// pane in this module already import; mixing the two Bubble Tea majors in
// one binary is not an option, and v1.4.7 is the newest tag still on the v1
// line (github.com/charmbracelet/wish's own tag list, checked 2026-08-20).
package sshserver

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"
)

// Handler builds the (tea.Model, []tea.ProgramOption) pair for one
// connecting SSH session -- called once per connection by wish's bubbletea
// middleware, never shared. Two attached clients each get their own
// Bubble Tea program and their own copy of whatever seed Model the caller
// closes over (internal/shell.Model's Update has a value receiver, so
// copying it per session is exactly what the Elm architecture already does
// on every keypress -- no separate synchronization needed for "who sees
// what", each SSH client just reads the same underlying fetchers
// independently, the same way two local -board runs would).
type Handler = bm.Handler

// Config controls one SSH listener serving Handler.
type Config struct {
	// Addr is the listen address, e.g. ":2222".
	Addr string
	// HostKeyPath is where the server's host key lives; wish generates an
	// ed25519 key here on first run if the file does not exist yet.
	HostKeyPath string
	// AuthorizedKeys is a path to an authorized_keys-format file (same
	// format OpenSSH's ~/.ssh/authorized_keys uses) -- only connections
	// presenting a matching key are admitted. Required unless Insecure is
	// explicitly set; ListenAndServe refuses to start otherwise; agent-tui#67
	// makes this Jon's decision, not a default an agent picks silently.
	AuthorizedKeys string
	// Insecure, if true, admits any client with no key check at all.
	// ListenAndServe requires this to be set explicitly when AuthorizedKeys
	// is empty specifically so an open listener can never be the silent
	// default -- see cmd/estate's -ssh-insecure flag, which logs a loud
	// warning every time this is true.
	Insecure bool
	// New builds the model+options for each connecting session.
	New Handler
}

// ListenAndServe blocks serving SSH connections at cfg.Addr until ctx is
// cancelled, then shuts the listener down gracefully (in-flight sessions get
// up to 5s to finish before a forced close) and returns nil. It returns
// immediately with an error if neither AuthorizedKeys nor Insecure is set,
// or if the underlying wish server fails to start (e.g. an unwritable
// HostKeyPath or a missing AuthorizedKeys file).
func ListenAndServe(ctx context.Context, cfg Config) error {
	if cfg.AuthorizedKeys == "" && !cfg.Insecure {
		return fmt.Errorf("sshserver: no AuthorizedKeys given and Insecure not set -- refusing to start an open SSH listener")
	}

	opts := []ssh.Option{
		wish.WithAddress(cfg.Addr),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		wish.WithMiddleware(
			bm.Middleware(cfg.New),
			logging.Middleware(),
		),
	}
	if cfg.Insecure {
		opts = append(opts, wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }))
	} else {
		opts = append(opts, wish.WithAuthorizedKeys(cfg.AuthorizedKeys))
	}

	s, err := wish.NewServer(opts...)
	if err != nil {
		return fmt.Errorf("sshserver: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.Shutdown(shutdownCtx)
	}
}

// MakeOptions exposes wish/bubbletea's per-session tea.ProgramOptions (pty
// size, output writer, etc.) so cmd/estate's Handler can append its own
// (tea.WithAltScreen, matching the local launch path in main.go) without
// this package needing to know what those are.
func MakeOptions(sess ssh.Session) []tea.ProgramOption {
	return bm.MakeOptions(sess)
}
