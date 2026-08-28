// This file is docs/SPEC-shell.md's S12: "A per-agent ExecutionMode:
// local (subprocess in a worktree, today's behaviour) or container
// (AgentBox docker sandbox)... Spec the interface in this item;
// implementing the container driver is its own later item."
//
// "Spec the interface" is taken literally: ExecutionMode and
// AddWithMode exist so a caller CAN ask for container mode, but nothing
// in agent-supervisor implements one yet -- confirmed by grepping
// agent-supervisor/scripts for "agentbox"/"docker" (zero matches,
// 2026-08-22) -- so AddWithMode(..., ExecutionContainer) returns
// ErrContainerNotImplemented rather than silently creating a local
// subprocess and calling it a container. That would be exactly the
// fabricated-success failure mode AGENTS.md's "never a fabricated
// metric" rule already forbids for a read; the same discipline applies
// here to a write that was asked to do one thing and cannot yet do it.
package session

import "errors"

// ExecutionMode is where a session's agent process actually runs.
// ExecutionLocal is every session this estate has ever created (a
// subprocess in a worktree, via bootstrap-session.sh, exactly what Add
// already does) -- the zero value, so an existing caller that never
// mentions ExecutionMode at all keeps getting exactly that.
type ExecutionMode string

const (
	ExecutionLocal     ExecutionMode = "local"
	ExecutionContainer ExecutionMode = "container"
)

// ErrContainerNotImplemented is AddWithMode's answer to
// ExecutionContainer today -- a real, typed refusal (AGENTS.md: "absence
// is a typed value, never a bare zero") rather than a session quietly
// created as local while reporting the mode the caller actually asked
// for.
var ErrContainerNotImplemented = errors.New("session: container execution mode is not implemented -- agent-tui SPEC-shell.md S12 specs the interface only, the driver is a later item")
