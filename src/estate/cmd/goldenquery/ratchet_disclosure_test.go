package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
)

// agent-estate#1208: the two failures a machine can actually detect in the
// ratchet's own "not ratcheted" disclosure and its #1209 exempt-count line
// are structural, not textual -- see the issue and its own follow-up
// comment for why an exact-prose test was rejected (it would not catch the
// failure this issue was filed about, and it would break on every
// legitimate reword). This test never compares reason text: it only checks
// that every un-ratcheted stratum still has *a* reason line, that the line
// still names an issue, and that the #1209 exempt-count line still prints
// unconditionally. Rewording any reason's prose while keeping its citation
// must keep this test green.
//
// It reads main.go's own source via go/parser rather than running the
// binary: main()'s reporting is not factored into a callable helper (this
// is a test-only change, per the issue's own constraint), and the report
// depends on a live `estate knowledge query` index this package's other
// tests do not stand up either.

var issueCitation = regexp.MustCompile(`#\d+`)

// mainFuncBody parses this package's own main.go and returns func main's
// statement list.
func mainFuncBody(t *testing.T) []ast.Stmt {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's own path")
	}
	mainPath := filepath.Join(filepath.Dir(thisFile), "main.go")

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, mainPath, nil, 0)
	if err != nil {
		t.Fatalf("parser.ParseFile(%s): %v", mainPath, err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" || fn.Recv != nil {
			continue
		}
		return fn.Body.List
	}
	t.Fatalf("main.go declares no func main()")
	return nil
}

// fprintLiteral returns the literal format string of a bare
// fmt.Fprintln(w, "...") / fmt.Fprintf(w, "...", ...) call, and whether the
// statement was exactly that shape.
func fprintLiteral(stmt ast.Stmt) (string, bool) {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" {
		return "", false
	}
	if sel.Sel.Name != "Fprintln" && sel.Sel.Name != "Fprintf" {
		return "", false
	}
	if len(call.Args) < 2 {
		return "", false
	}
	target, ok := call.Args[0].(*ast.Ident)
	if !ok || target.Name != "w" {
		return "", false
	}
	lit, ok := call.Args[1].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// ratchetDisclosureLines locates the block of plain fmt.Fprint{ln,f}(w, ...)
// statements that follow the `for _, r := range ratchets` reporting loop in
// main() -- these are the "not ratcheted" disclosure lines -- and the
// if/else immediately after them, which is #1209's exempt-count line.
func ratchetDisclosureLines(t *testing.T) (reasons []string, exemptThen, exemptElse string) {
	t.Helper()
	stmts := mainFuncBody(t)

	loopIdx := -1
	for i, stmt := range stmts {
		rng, ok := stmt.(*ast.RangeStmt)
		if !ok {
			continue
		}
		if x, ok := rng.X.(*ast.Ident); ok && x.Name == "ratchets" {
			loopIdx = i
			break
		}
	}
	if loopIdx == -1 {
		t.Fatal("main() no longer has a `for _, r := range ratchets` loop -- ratchetDisclosureLines can't locate the disclosure lines relative to it")
	}

	i := loopIdx + 1
	for ; i < len(stmts); i++ {
		s, ok := fprintLiteral(stmts[i])
		if !ok {
			break
		}
		reasons = append(reasons, s)
	}
	if i >= len(stmts) {
		t.Fatal("main() ends right after the ratchet loop's disclosure lines -- expected the #1209 exempt-count if/else to follow")
	}
	ifStmt, ok := stmts[i].(*ast.IfStmt)
	if !ok {
		t.Fatalf("statement after the ratchet disclosure lines is a %T, want *ast.IfStmt (the #1209 exempt-count line)", stmts[i])
	}
	if ifStmt.Else == nil {
		t.Fatal("#1209 exempt-count if has no else branch -- it must print even when exemptCount == 0")
	}

	thenLit, ok := soleFprint(ifStmt.Body.List)
	if !ok {
		t.Fatal("#1209 exempt-count if-branch does not contain a bare fmt.Fprint{ln,f}(w, ...) call")
	}
	exemptThen = thenLit

	elseBlock, ok := ifStmt.Else.(*ast.BlockStmt)
	if !ok {
		t.Fatalf("#1209 exempt-count else branch is a %T, want *ast.BlockStmt", ifStmt.Else)
	}
	elseLit, ok := soleFprint(elseBlock.List)
	if !ok {
		t.Fatal("#1209 exempt-count else-branch does not contain a bare fmt.Fprint{ln,f}(w, ...) call")
	}
	exemptElse = elseLit

	return reasons, exemptThen, exemptElse
}

func soleFprint(stmts []ast.Stmt) (string, bool) {
	for _, s := range stmts {
		if lit, ok := fprintLiteral(s); ok {
			return lit, true
		}
	}
	return "", false
}

// TestNotRatchetedStrataEachCiteAnIssue is guardrail 1 from agent-estate#1208:
// every stratum the ratchet declines to guard must still explain itself, and
// that explanation must name the issue a reader can trace it to. Deleting a
// disclosure line, or stripping its citation while leaving the prose, must
// fail this test; rewording the prose while keeping the citation must not.
func TestNotRatchetedStrataEachCiteAnIssue(t *testing.T) {
	reasons, _, _ := ratchetDisclosureLines(t)

	const wantMinReasonLines = 2 // agent-estate#1112 drift line + agent-estate#1066/#1115 term-overlap line, measured on this branch
	if len(reasons) < wantMinReasonLines {
		t.Fatalf("found %d 'not ratcheted' disclosure line(s) after the ratchet loop, want at least %d -- a disclosure line for an un-ratcheted stratum appears to have been removed:\n%q", len(reasons), wantMinReasonLines, reasons)
	}

	for i, r := range reasons {
		if !issueCitation.MatchString(r) {
			t.Errorf("disclosure line %d carries no issue citation (want something matching %s): %q", i, issueCitation.String(), r)
		}
	}
}

// TestExemptCountLineAlwaysPrintsAndCitesItsIssue is guardrail 2: the
// agent-estate#1209 exempt-count line must print on both the zero-count and
// nonzero-count paths, and whichever branch fires must still cite the issue
// that justifies the exemption mechanism. Presence and citation are what
// matter -- not the wording used to report the count.
func TestExemptCountLineAlwaysPrintsAndCitesItsIssue(t *testing.T) {
	_, thenLit, elseLit := ratchetDisclosureLines(t)

	if thenLit == "" {
		t.Error("exempt-count if-branch (exemptCount > 0) printed an empty string")
	}
	if elseLit == "" {
		t.Error("exempt-count else-branch (exemptCount == 0) printed an empty string")
	}
	if !issueCitation.MatchString(thenLit) {
		t.Errorf("exempt-count if-branch carries no issue citation: %q", thenLit)
	}
	if !issueCitation.MatchString(elseLit) {
		t.Errorf("exempt-count else-branch carries no issue citation: %q", elseLit)
	}
}
