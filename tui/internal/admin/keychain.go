package admin

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// ExecKeychainProbe builds a real KeychainRunner over the macOS `security`
// CLI -- agent-tui#149's own proposal. It is a READ probe only, and must
// stay that way: this repo's AGENTS.md states, without exception, that a
// credential store is read-only, because `security add-generic-password
// -U -A` once destroyed a live credential's ACL and locked every agent in
// this estate out for a day. This function must never grow an -A/-U/add/
// delete path, no matter how tempting "fix the row it just reported" is.
//
//   - Never asks for the value. `-w` (or `-g`) would print the secret to
//     stdout; this command omits both, and both Stdout and Stderr are left
//     nil (discarded, never captured into a Go string) so nothing the
//     keychain returns -- value or metadata -- can reach this pane, a log,
//     or an error string, matching the issue's own "do not leak the
//     credential or its contents" requirement.
//   - Never prompts. `security find-generic-password` does not itself pop
//     a GUI authorization dialog for a missing/locked item; it returns a
//     non-zero exit (or, per the outage this issue is grounded in, blocks
//     for minutes). The timeout below is what turns that block into an
//     honest "could not determine" instead of hanging the whole pane --
//     the issue's own "bound it and render a timeout as could not
//     determine, not as green."
//   - service names the keychain item to check ("Claude Code-credentials"
//     in a real wiring -- the item every lane authenticates against, per
//     the issue). timeout bounds the whole call.
//
// Known gap, stated rather than silently assumed away: this runs the
// probe in THIS process's own security context, not the tmux server's
// (agent-supervisor#582's own finding was that the two can answer
// differently, which is why the issue asked for `tmux run-shell -b`
// specifically). AGENTS.md's own "What NOT to do here" forbids any
// internal/ package calling os/exec for tmux directly -- every
// tmux-adjacent operation is a supervisor MCP tool call, and adding that
// call is out of this change's stated scope (internal/admin and its
// tests only). A row answered from the TUI's own process is still a real
// improvement over no row at all, but it is not a lane-equivalent
// answer -- see this package's own doc comment and this change's PR
// description for that caveat stated plainly, not implied.
func ExecKeychainProbe(service string, timeout time.Duration) KeychainRunner {
	return func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "security", "find-generic-password", "-s", service)
		// Discard both streams unread -- see this function's own doc
		// comment on why nothing `security` prints may be captured.
		cmd.Stdout = nil
		cmd.Stderr = nil

		err := cmd.Run()
		if ctx.Err() != nil {
			// Timed out (or was otherwise cancelled) before `security`
			// answered -- a blocked/locked keychain, per the outage this
			// issue is grounded in. Could not determine, never rendered
			// as a definite "no".
			return false, ctx.Err()
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Ran to completion and refused -- locked, denied, or the
			// item does not exist. A definite, cheap-to-trust "no".
			return false, nil
		}
		if err != nil {
			// Could not even start the probe (e.g. `security` missing
			// from $PATH) -- could not determine, the same as a timeout.
			return false, err
		}
		return true, nil
	}
}
