// Package harness makes a dispatched turn harness-neutral.
//
// WHY. `estate dispatch` ran `claude -p` and nothing else, which makes this a
// harness, not a meta-harness. Orchestrating agentic workloads ACROSS
// harnesses and models is the product; one hardcoded binary is the thing the
// product is supposed to replace.
//
// A harness here owns three things and nothing else: how to build the command
// for one turn, how to read the agent's final message out of what that
// command produced, and whether it confines the agent's writes itself. The
// caller owns everything else -- the ledger, the gate, the worktree -- so
// adding a harness never touches dispatch's logic.
//
// SANDBOXING IS NOT UNIFORM ACROSS HARNESSES, and this package refuses to
// paper over that. `claude -p --dangerously-skip-permissions` has no sandbox:
// the worktree bounds its git working tree, not its filesystem access.
// `codex exec --sandbox workspace-write` confines writes to the working
// directory. Sandboxed() reports the difference so a caller can choose, and
// so nothing here implies a guarantee one of them does not make.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Turn is one prepared agent turn: a command to run, a way to read its
// result, and any cleanup the harness needs afterwards.
type Turn struct {
	Cmd *exec.Cmd
	// Result extracts the agent's final message from the finished command's
	// stdout. An error means the turn's output could not be read, which is
	// NOT the same as the turn failing -- the caller records that distinction
	// as "unknown" rather than "failed".
	Result func(stdout []byte) (string, error)
	// Cleanup releases anything Start allocated. Always safe to call.
	Cleanup func()
}

// Harness is one agent CLI this estate can dispatch to.
type Harness interface {
	Name() string
	// Sandboxed reports whether the harness itself confines the agent's
	// writes to its working directory. False means the only containment is
	// whatever the caller arranged.
	Sandboxed() bool
	// Start prepares a turn to run in dir with the given prompt.
	Start(ctx context.Context, dir, prompt string) (*Turn, error)
}

var registry = map[string]Harness{}

func register(h Harness) { registry[h.Name()] = h }

func init() {
	register(claude{})
	register(codex{})
}

// Lookup returns the named harness, or an error naming what is available.
// An unknown harness is refused rather than defaulted: silently falling back
// to claude would run a turn on a harness the caller did not choose.
func Lookup(name string) (Harness, error) {
	if h, ok := registry[name]; ok {
		return h, nil
	}
	return nil, fmt.Errorf("unknown harness %q; available: %s", name, strings.Join(Names(), ", "))
}

// Names lists every registered harness, sorted.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Available reports whether the harness's binary is on PATH. A harness that
// is registered but not installed is a real state and must be distinguishable
// from one that does not exist.
func Available(name string) (bool, error) {
	if _, err := Lookup(name); err != nil {
		return false, err
	}
	_, err := exec.LookPath(binaryFor(name))
	return err == nil, nil
}

func binaryFor(name string) string { return name }

// --- claude ------------------------------------------------------------

type claude struct{}

func (claude) Name() string { return "claude" }

// Sandboxed is false, and deliberately so. --dangerously-skip-permissions is
// exactly what it says: no sandbox, no approval prompts. The worktree the
// caller supplies bounds the agent's git working tree, not its filesystem.
func (claude) Sandboxed() bool { return false }

func (claude) Start(ctx context.Context, dir, prompt string) (*Turn, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "json",
		"--dangerously-skip-permissions")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	return &Turn{
		Cmd:     cmd,
		Result:  claudeResult,
		Cleanup: func() {},
	}, nil
}

// claudeResult reads claude -p --output-format json's envelope. Exiting 0
// with output we cannot parse is not a clean completion, so it errors rather
// than returning an empty result that would read as a successful empty turn.
func claudeResult(stdout []byte) (string, error) {
	var parsed map[string]any
	if err := json.Unmarshal(stdout, &parsed); err != nil {
		return "", fmt.Errorf("claude: exit 0 but result was not parseable JSON: %w", err)
	}
	s, ok := parsed["result"].(string)
	if !ok {
		return "", fmt.Errorf("claude: JSON envelope had no string \"result\" field")
	}
	return s, nil
}

// --- codex -------------------------------------------------------------

type codex struct{}

func (codex) Name() string { return "codex" }

// Sandboxed is true: codex exec --sandbox workspace-write confines
// model-generated shell commands to the working directory. That is a stronger
// containment than the claude adapter can offer today, and it is the reason
// this seam reports the property instead of assuming every harness is alike.
func (codex) Sandboxed() bool { return true }

func (codex) Start(ctx context.Context, dir, prompt string) (*Turn, error) {
	// codex writes its final message to a file rather than emitting a single
	// JSON envelope on stdout, so the result is read from there.
	f, err := os.CreateTemp("", "estate-codex-*.txt")
	if err != nil {
		return nil, fmt.Errorf("codex: cannot make a file for the turn's result: %w", err)
	}
	path := f.Name()
	f.Close()

	cmd := exec.CommandContext(ctx, "codex", "exec",
		"--sandbox", "workspace-write",
		"--skip-git-repo-check",
		"--output-last-message", path,
		"-")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)

	return &Turn{
		Cmd: cmd,
		Result: func([]byte) (string, error) {
			b, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("codex: exit 0 but the turn's result file was unreadable: %w", err)
			}
			if strings.TrimSpace(string(b)) == "" {
				return "", fmt.Errorf("codex: exit 0 but the turn's result file was empty")
			}
			return strings.TrimSpace(string(b)), nil
		},
		Cleanup: func() { os.Remove(path) },
	}, nil
}
