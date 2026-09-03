package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/mirror"
	"github.com/jonhill90/agent-estate/estate/internal/pressure"
)

// A mechanism nothing calls is a documentation rule with a binary attached --
// this repo has been bitten by exactly that (count-agents.sh existed, was
// tested, and for a time nothing invoked it). internal/mirror is only worth
// anything if dispatch actually tees the turn's streams into it, so the
// wiring itself is checked here rather than assumed.
//
// These read main.go's source because the alternative -- running a real
// dispatch -- needs a harness, a corpus, a worktree and real spend, which no
// unit test should require. A source check is weaker than an execution check
// and is not claimed to be more: it catches the mirror being UNWIRED, not the
// mirror being wired wrongly. The live end-to-end evidence for the latter is
// in the pull request.

// mainSource is main.go with its // comments removed, so these guards read
// the code rather than the prose about it -- the mirror block's own comments
// name several of the verbs the guards below ban, on purpose.
func mainSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	return stripGoComments(string(b))
}

func stripGoComments(src string) string {
	var out strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if i := strings.Index(line, "// "); i >= 0 {
			line = line[:i]
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

func TestDispatchTeesBothStreamsIntoTheMirror(t *testing.T) {
	src := mainSource(t)
	for _, want := range []string{
		"cmd.Stdout = io.MultiWriter(&stdout, mir.Stdout())",
		"cmd.Stderr = io.MultiWriter(&stderrBuf, mir.Stderr())",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("dispatch no longer tees a stream into the mirror; expected %q in main.go", want)
		}
	}
	// The transport's own capture must survive the tee: Result, Spend and
	// SessionID are all read from the stdout buffer, and a mirror that
	// replaced rather than duplicated it would silently blind all three.
	if !strings.Contains(src, "&stdout,") || !strings.Contains(src, "&stderrBuf,") {
		t.Fatalf("the mirror replaced the transport's own capture instead of duplicating it")
	}
}

func TestDispatchStampsATerminalStateIntoTheTranscript(t *testing.T) {
	src := mainSource(t)
	if !strings.Contains(src, `mir.Close(string(rec.State), rec.Note)`) {
		t.Fatalf("dispatch does not stamp the recorded state into the transcript; a dead turn's pane would trail off mid-output")
	}
	if !strings.Contains(src, `defer mir.Close("unknown", "dispatch exited before recording a terminal state")`) {
		t.Fatalf("the catch-all deferred Close is gone; a path that returns rather than exits would leave the transcript unstamped")
	}
}

// TestEveryExitInsideTheMirrorRegionStampsTheTranscript ENUMERATES the exits
// rather than asserting that some known Close calls are present.
//
// The difference is the whole point. Its predecessor checked that two
// particular mir.Close strings appeared in main.go -- which says nothing
// about a THIRD path that has no Close at all, and review of #1003 found
// exactly that: h.Start's error path exited with the transcript unstamped,
// while both the PR body and main.go's own comment said there were two paths
// and both closed. A presence check cannot see an omission; a census can.
//
// The rule enforced: between mirror.Open and the ledger append, every
// os.Exit must be preceded, in its own branch, by a NON-DEFERRED mir.Close.
// Deferred calls are ignored on purpose -- os.Exit skips defers, which is the
// entire reason this class of bug exists.
func TestEveryExitInsideTheMirrorRegionStampsTheTranscript(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}

	var open, lastClose token.Pos
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isSelector(call.Fun, "mirror", "Open") {
			open = call.Pos()
		}
		if isSelector(call.Fun, "mir", "Close") && call.Pos() > lastClose {
			lastClose = call.Pos()
		}
		return true
	})
	if !open.IsValid() {
		t.Fatalf("dispatch no longer opens a mirror at all")
	}
	if lastClose <= open {
		t.Fatalf("no mir.Close follows mirror.Open; nothing stamps a terminal state")
	}

	// Walk the function bodies, tracking whether a non-deferred mir.Close has
	// already run on the path reaching each os.Exit.
	var unstamped []string
	var scan func(stmts []ast.Stmt, closed bool)
	scanStmt := func(s ast.Stmt, closed bool) { scan([]ast.Stmt{s}, closed) }
	scan = func(stmts []ast.Stmt, closed bool) {
		for _, s := range stmts {
			// A defer does not run on os.Exit, so it stamps nothing and its
			// body is not a path.
			if _, isDefer := s.(*ast.DeferStmt); isDefer {
				continue
			}
			switch st := s.(type) {
			case *ast.BlockStmt:
				scan(st.List, closed)
			case *ast.IfStmt:
				scan(st.Body.List, closed)
				if st.Else != nil {
					scanStmt(st.Else, closed)
				}
			case *ast.ForStmt:
				scan(st.Body.List, closed)
			case *ast.RangeStmt:
				scan(st.Body.List, closed)
			case *ast.SwitchStmt:
				scan(st.Body.List, closed)
			case *ast.TypeSwitchStmt:
				scan(st.Body.List, closed)
			case *ast.SelectStmt:
				scan(st.Body.List, closed)
			case *ast.CaseClause:
				scan(st.Body, closed)
			case *ast.CommClause:
				scan(st.Body, closed)
			default:
				ast.Inspect(s, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if isSelector(call.Fun, "os", "Exit") && call.Pos() > open && call.Pos() < lastClose && !closed {
						unstamped = append(unstamped, fset.Position(call.Pos()).String())
					}
					return true
				})
			}
			if stmtClosesTheMirror(s) {
				closed = true
			}
		}
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		scan(fn.Body.List, false)
	}

	if len(unstamped) > 0 {
		t.Fatalf("%d exit(s) between mirror.Open and the ledger append leave the transcript unstamped -- "+
			"a human opening that pane afterwards sees output trailing off with no terminal state: %s",
			len(unstamped), strings.Join(unstamped, ", "))
	}
	// The census must be measuring something: if it found no exits at all in
	// the region, it would pass by looking at nothing.
	if !strings.Contains(mainSource(t), "os.Exit") {
		t.Fatalf("no os.Exit anywhere in main.go; this guard is measuring nothing")
	}
}

// stmtClosesTheMirror reports a non-deferred mir.Close call in this statement.
func stmtClosesTheMirror(s ast.Stmt) bool {
	if _, isDefer := s.(*ast.DeferStmt); isDefer {
		return false
	}
	found := false
	ast.Inspect(s, func(n ast.Node) bool {
		if _, isDefer := n.(*ast.DeferStmt); isDefer {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && isSelector(call.Fun, "mir", "Close") {
			found = true
		}
		return true
	})
	return found
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// The pane bound and the concurrency bound must be the SAME number, not two
// numbers that happen to agree today. A pane per concurrent turn is bounded;
// a pane per historical turn is not, and 208 panes is the 176-worktree
// OOM defect wearing a different hat.
func TestThePaneBoundIsTheInFlightCap(t *testing.T) {
	cap := pressure.Default().MaxInFlight
	if got := mirror.Default(cap).Max; got != cap {
		t.Fatalf("mirror bound %d does not track the in-flight cap %d", got, cap)
	}
	if !strings.Contains(mainSource(t), "mirror.Default(pressure.Default().MaxInFlight)") {
		t.Fatalf("dispatch no longer derives the pane bound from the in-flight cap; the two can now drift")
	}
}

// Visibility must never gate work. A mirror that cannot be opened -- no tmux,
// or a bound full of live turns -- is reported and the dispatch proceeds.
func TestAMirrorFailureDoesNotStopTheDispatch(t *testing.T) {
	src := mainSource(t)
	i := strings.Index(src, "mir, merr := mirror.Open(")
	if i < 0 {
		t.Fatalf("dispatch no longer opens a mirror at all")
	}
	// Look at the refusal branch only.
	tail := src[i:]
	end := strings.Index(tail, "ctx, cancel := context.WithTimeout")
	if end < 0 {
		t.Fatalf("could not find the end of the mirror block in main.go")
	}
	block := tail[:end]
	if strings.Contains(block, "os.Exit") {
		t.Fatalf("a mirror failure exits the dispatch; visibility must never gate work:\n%s", block)
	}
}

// The estate must never type into a mirror pane. internal/mirror enforces
// that at the point a tmux command is built (its allowedVerbs allowlist,
// which no file or spelling can walk around); this is the caller-side half --
// nothing in package main may reach around the package to tmux at all.
//
// Reach, stated rather than implied: this reads EVERY non-test .go file in
// this directory, not just main.go, because a control path in a sibling file
// of the same package used to pass green. It matches literal strings, so a
// verb or a binary name assembled at runtime is invisible to it. That hole is
// not closed here and is not claimed to be; what closes it for the tmux
// commands this system actually issues is internal/mirror's allowlist, and
// what closes it for main.go specifically is that main.go has no os/exec call
// naming tmux to build on.
func TestDispatchHasNoTmuxControlPath(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatalf("reading %s: %v", name, rerr)
		}
		src := stripGoComments(string(b))
		scanned++
		for _, banned := range []string{"send-keys", "kill-server", "kill-session", "\"tmux\""} {
			if strings.Contains(src, banned) {
				t.Fatalf("%s references %q -- tmux is reached only through internal/mirror, and only as a viewer", name, banned)
			}
		}
	}
	if scanned == 0 {
		t.Fatalf("the package scan found no source files -- the guard is measuring nothing")
	}
}
