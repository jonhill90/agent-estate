package session

import (
	"errors"
	"testing"
)

// fakeCallTooler is a CallTooler with no MCP subprocess involved at all --
// every test below asserts exactly which tool name and arguments Ops sent,
// and controls exactly what comes back, the same discipline
// internal/mcp.Client's own tests use for the transport layer one level
// down.
type fakeCallTooler struct {
	gotName string
	gotArgs map[string]any
	text    string
	err     error
}

func (f *fakeCallTooler) CallTool(name string, arguments map[string]any) (string, error) {
	f.gotName = name
	f.gotArgs = arguments
	return f.text, f.err
}

func TestAttachCallsSessionAttachWithSessionName(t *testing.T) {
	fake := &fakeCallTooler{text: `{"session":"scratch","attached":true}`}
	o := New(fake)
	if err := o.Attach("scratch"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if fake.gotName != "session_attach" {
		t.Fatalf("tool = %q, want session_attach", fake.gotName)
	}
	if fake.gotArgs["session"] != "scratch" {
		t.Fatalf("args = %+v, want session=scratch", fake.gotArgs)
	}
}

func TestAttachSurfacesToolError(t *testing.T) {
	fake := &fakeCallTooler{err: errors.New("mcp: session_attach: tmux has no session named 'gone'")}
	o := New(fake)
	if err := o.Attach("gone"); err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func TestDetachCallsSessionDetachWithNoArguments(t *testing.T) {
	fake := &fakeCallTooler{text: `{"detached":true}`}
	o := New(fake)
	if err := o.Detach(); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if fake.gotName != "session_detach" {
		t.Fatalf("tool = %q, want session_detach", fake.gotName)
	}
	if len(fake.gotArgs) != 0 {
		t.Fatalf("args = %+v, want none", fake.gotArgs)
	}
}

// TestAddOmitsUnsetOptionalArguments is the load-bearing case for
// session_add's contract: lanes<=0/agent=""/cwd="" must NOT appear in the
// arguments at all, so bootstrap-session.sh's own defaults apply
// server-side -- sending lanes:0 would instead ask for a zero-lane session,
// a different (and refused) request.
func TestAddOmitsUnsetOptionalArguments(t *testing.T) {
	fake := &fakeCallTooler{text: `{"session":"scratch","created":true,"state":"supervised","bootstrap_output":""}`}
	o := New(fake)
	if _, err := o.Add("scratch", 0, "", ""); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, ok := fake.gotArgs["lanes"]; ok {
		t.Fatalf("args = %+v, lanes must be omitted when <= 0", fake.gotArgs)
	}
	if _, ok := fake.gotArgs["agent"]; ok {
		t.Fatalf("args = %+v, agent must be omitted when empty", fake.gotArgs)
	}
	if _, ok := fake.gotArgs["cwd"]; ok {
		t.Fatalf("args = %+v, cwd must be omitted when empty", fake.gotArgs)
	}
}

func TestAddPassesOptionalArgumentsWhenSet(t *testing.T) {
	fake := &fakeCallTooler{text: `{"session":"scratch","created":true,"state":"supervised","bootstrap_output":""}`}
	o := New(fake)
	if _, err := o.Add("scratch", 4, "claude", "/work"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fake.gotArgs["lanes"] != 4 {
		t.Fatalf("args[lanes] = %v, want 4", fake.gotArgs["lanes"])
	}
	if fake.gotArgs["agent"] != "claude" {
		t.Fatalf("args[agent] = %v, want claude", fake.gotArgs["agent"])
	}
	if fake.gotArgs["cwd"] != "/work" {
		t.Fatalf("args[cwd] = %v, want /work", fake.gotArgs["cwd"])
	}
}

// TestAddDecodesTheResultingSupervisionState is agent-tui#14 acceptance
// item 2 at this package's boundary: "add creates a session that reads
// supervised" is only true if this decode round-trips session_add's
// "state" field intact.
func TestAddDecodesTheResultingSupervisionState(t *testing.T) {
	fake := &fakeCallTooler{text: `{"session":"scratch","created":true,"state":"supervised","bootstrap_output":"bootstrap-session: 4 created, 0 left alone"}`}
	o := New(fake)
	result, err := o.Add("scratch", 0, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if result.State != "supervised" {
		t.Fatalf("State = %q, want supervised", result.State)
	}
	if !result.Created {
		t.Fatalf("Created = false, want true")
	}
}

func TestRemoveCheckDecodesRefusals(t *testing.T) {
	fake := &fakeCallTooler{text: `{
		"session":"hill90","exists":true,"supervision":"unknown","busy_lanes":[],
		"worktrees":[],"safe_to_remove":false,
		"refusals":["session is not supervised (as#153 marker reads unknown)"]
	}`}
	o := New(fake)
	check, err := o.RemoveCheck("hill90")
	if err != nil {
		t.Fatalf("RemoveCheck: %v", err)
	}
	if check.SafeToRemove {
		t.Fatalf("SafeToRemove = true, want false")
	}
	if len(check.Refusals) != 1 || check.Refusals[0] == "" {
		t.Fatalf("Refusals = %+v, want one non-empty reason", check.Refusals)
	}
}

// TestRemoveCheckPreservesUndeterminableAsNil is agent-tui#14 requirement 3
// at this package's decode boundary: "cannot tell" must decode as nil, not
// as false (which would render as "clean").
func TestRemoveCheckPreservesUndeterminableAsNil(t *testing.T) {
	fake := &fakeCallTooler{text: `{
		"session":"scratch","exists":true,"supervision":"supervised","busy_lanes":[],
		"worktrees":[{"path":"/work/scratch","clean":null,"unpushed":null,"reason":"git status failed: exit 128"}],
		"safe_to_remove":false,
		"refusals":["worktree /work/scratch: cannot determine (git status failed: exit 128)"]
	}`}
	o := New(fake)
	check, err := o.RemoveCheck("scratch")
	if err != nil {
		t.Fatalf("RemoveCheck: %v", err)
	}
	if len(check.Worktrees) != 1 {
		t.Fatalf("Worktrees = %+v, want 1", check.Worktrees)
	}
	wt := check.Worktrees[0]
	if wt.Clean != nil {
		t.Fatalf("Clean = %v, want nil (undeterminable)", *wt.Clean)
	}
	if wt.Unpushed != nil {
		t.Fatalf("Unpushed = %v, want nil (undeterminable)", *wt.Unpushed)
	}
	if wt.Reason == "" {
		t.Fatal("Reason is empty for an undeterminable worktree")
	}
}

func TestRemoveSendsConfirmTrue(t *testing.T) {
	fake := &fakeCallTooler{text: `{"session":"scratch","removed":true,"guard":{"session":"scratch","exists":true,"supervision":"supervised","busy_lanes":[],"worktrees":[],"safe_to_remove":true,"refusals":[]}}`}
	o := New(fake)
	result, err := o.Remove("scratch", true)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if fake.gotArgs["confirm"] != true {
		t.Fatalf("args[confirm] = %v, want true", fake.gotArgs["confirm"])
	}
	if fake.gotArgs["session"] != "scratch" {
		t.Fatalf("args[session] = %v, want scratch", fake.gotArgs["session"])
	}
	if !result.Removed {
		t.Fatal("Removed = false, want true")
	}
}

// TestRemoveWithoutConfirmStillReachesTheServer -- this package never
// short-circuits confirm=false locally into "success"; the supervisor is
// the one place that must refuse it (agent-tui#14's own architectural
// rule: safety lives in one place). A local short-circuit here would be a
// second, divergeable copy of that refusal.
func TestRemoveWithoutConfirmStillReachesTheServer(t *testing.T) {
	fake := &fakeCallTooler{err: errors.New("mcp: session_remove: session_remove requires confirm=true (code -32602)")}
	o := New(fake)
	if _, err := o.Remove("scratch", false); err == nil {
		t.Fatal("expected an error surfaced from the server, got nil")
	}
	if fake.gotArgs["confirm"] != false {
		t.Fatalf("args[confirm] = %v, want false (sent through, not swallowed)", fake.gotArgs["confirm"])
	}
}

func TestRemoveSurfacesRefusalError(t *testing.T) {
	fake := &fakeCallTooler{err: errors.New("mcp: session_remove: refuses to remove hill90: session is not supervised")}
	o := New(fake)
	if _, err := o.Remove("hill90", true); err == nil {
		t.Fatal("expected a refusal error, got nil")
	}
}

// TestAddWithModeLocalCallsSessionAddIdentically covers SPEC-shell.md
// S12's own explicit requirement -- ExecutionLocal is not a second code
// path, it is the same session_add call Add already makes.
func TestAddWithModeLocalCallsSessionAddIdentically(t *testing.T) {
	fake := &fakeCallTooler{text: `{"session":"scratch","created":true,"state":"supervised","bootstrap_output":""}`}
	o := New(fake)
	if _, err := o.AddWithMode("scratch", 4, "claude", "/work", ExecutionLocal); err != nil {
		t.Fatalf("AddWithMode: %v", err)
	}
	if fake.gotName != "session_add" {
		t.Fatalf("gotName = %q, want %q", fake.gotName, "session_add")
	}
	if fake.gotArgs["lanes"] != 4 || fake.gotArgs["agent"] != "claude" || fake.gotArgs["cwd"] != "/work" {
		t.Fatalf("args = %+v, want lanes=4 agent=claude cwd=/work", fake.gotArgs)
	}
}

// TestAddWithModeEmptyStringDefaultsToLocal is ExecutionMode's own zero
// value contract -- a caller that never sets Mode at all must behave
// exactly like ExecutionLocal, not fail or take a third path.
func TestAddWithModeEmptyStringDefaultsToLocal(t *testing.T) {
	fake := &fakeCallTooler{text: `{"session":"scratch","created":true,"state":"supervised","bootstrap_output":""}`}
	o := New(fake)
	if _, err := o.AddWithMode("scratch", 0, "", "", ""); err != nil {
		t.Fatalf("AddWithMode with the zero-value ExecutionMode: %v", err)
	}
	if fake.gotName != "session_add" {
		t.Fatalf("gotName = %q, want %q", fake.gotName, "session_add")
	}
}

// TestAddWithModeContainerIsNotImplemented is execution_mode.go's own
// central contract: no CallTool at all, and the specific typed error, not
// a generic failure or (worse) a session created as local while claiming
// container.
func TestAddWithModeContainerIsNotImplemented(t *testing.T) {
	fake := &fakeCallTooler{text: `{"session":"scratch","created":true,"state":"supervised","bootstrap_output":""}`}
	o := New(fake)
	_, err := o.AddWithMode("scratch", 0, "", "", ExecutionContainer)
	if !errors.Is(err, ErrContainerNotImplemented) {
		t.Fatalf("AddWithMode(..., ExecutionContainer) error = %v, want ErrContainerNotImplemented", err)
	}
	if fake.gotName != "" {
		t.Fatalf("gotName = %q, want no CallTool at all for an unimplemented mode", fake.gotName)
	}
}

func TestAddWithModeUnknownModeIsARealError(t *testing.T) {
	fake := &fakeCallTooler{}
	o := New(fake)
	if _, err := o.AddWithMode("scratch", 0, "", "", ExecutionMode("quantum")); err == nil {
		t.Fatal("expected an error for an unknown ExecutionMode, got nil")
	}
}
