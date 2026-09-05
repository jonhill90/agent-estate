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

// parsedMain parses this package's own main.go once and returns both the
// parsed file (needed to resolve a helper-function call, see
// helperFormatLiteral) and func main's own statement list.
func parsedMain(t *testing.T) (*ast.File, []ast.Stmt) {
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
		return f, fn.Body.List
	}
	t.Fatalf("main.go declares no func main()")
	return nil, nil
}

// mainFuncBody is parsedMain for the callers that only need the statement
// list, not the file (every caller in this file except
// TestCorpusGrowthDriftLineIsFedUnscopedTotal, which resolves a call
// argument's own identifier and needs neither).
func mainFuncBody(t *testing.T) []ast.Stmt {
	t.Helper()
	_, stmts := parsedMain(t)
	return stmts
}

// fprintLiteral returns the literal format string of a bare
// fmt.Fprintln(w, "...") / fmt.Fprintf(w, "...", ...) call, and whether the
// statement was exactly that shape. agent-estate#1218: main.go's disclosure
// lines are no longer all inline string literals -- publishableReachableLine
// (agent-estate#1214) and corpusGrowthDriftLine (agent-estate#1218) factored
// two of them into small pure functions, so this now also recognises
// fmt.Fprintln(w, someHelper(...)) and resolves the literal from that
// helper's own `return fmt.Sprintf("...", ...)` in the same file, via
// helperFormatLiteral -- keeping the #1208 citation guardrails working
// across the factor-out rather than only across a literal reword.
func fprintLiteral(f *ast.File, stmt ast.Stmt) (string, bool) {
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
	if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return "", false
		}
		return s, true
	}
	// Not a bare literal -- see if it's a call to a helper function
	// factored out of main() in this same file.
	helperCall, ok := call.Args[1].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	helperIdent, ok := helperCall.Fun.(*ast.Ident)
	if !ok {
		return "", false
	}
	return helperFormatLiteral(f, helperIdent.Name)
}

// helperFormatLiteral looks up the top-level function named name in f and
// returns the literal format string of its `return fmt.Sprintf("...", ...)`
// statement -- the shape both publishableReachableLine and
// corpusGrowthDriftLine use. This is how fprintLiteral keeps working after a
// disclosure line moves from an inline literal into a small pure formatting
// helper: the citation still lives in source, just one function down.
func helperFormatLiteral(f *ast.File, name string) (string, bool) {
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Recv != nil {
			continue
		}
		for _, stmt := range fn.Body.List {
			ret, ok := stmt.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				continue
			}
			call, ok := ret.Results[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "fmt" || sel.Sel.Name != "Sprintf" {
				continue
			}
			if len(call.Args) == 0 {
				continue
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			return s, true
		}
		return "", false
	}
	return "", false
}

// ratchetDisclosureLines locates the block of plain fmt.Fprint{ln,f}(w, ...)
// statements that follow the `for _, r := range ratchets` reporting loop in
// main() -- these are the "not ratcheted" disclosure lines -- and the
// if/else immediately after them, which is #1209's exempt-count line.
func ratchetDisclosureLines(t *testing.T) (reasons []string, exemptThen, exemptElse string) {
	t.Helper()
	f, stmts := parsedMain(t)

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
		s, ok := fprintLiteral(f, stmts[i])
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

	thenLit, ok := soleFprint(f, ifStmt.Body.List)
	if !ok {
		t.Fatal("#1209 exempt-count if-branch does not contain a bare fmt.Fprint{ln,f}(w, ...) call")
	}
	exemptThen = thenLit

	elseBlock, ok := ifStmt.Else.(*ast.BlockStmt)
	if !ok {
		t.Fatalf("#1209 exempt-count else branch is a %T, want *ast.BlockStmt", ifStmt.Else)
	}
	elseLit, ok := soleFprint(f, elseBlock.List)
	if !ok {
		t.Fatal("#1209 exempt-count else-branch does not contain a bare fmt.Fprint{ln,f}(w, ...) call")
	}
	exemptElse = elseLit

	return reasons, exemptThen, exemptElse
}

func soleFprint(f *ast.File, stmts []ast.Stmt) (string, bool) {
	for _, s := range stmts {
		if lit, ok := fprintLiteral(f, s); ok {
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

// TestMainCallsCorpusGrowthDriftLineWithNlUnscopedResults is PR #1219's
// fix-pass rename and rewrite of agent-estate#1218's
// TestMainCallsCorpusGrowthDriftLineWithNlTotal, following the same
// "refactor, don't just re-point" move agent-estate#1218 itself made against
// agent-estate#1215's TestCorpusGrowthDriftLineIsFedUnscopedTotal.
//
// agent-estate#1218's own finding against the ORIGINAL (#1215) test: it
// asserted on the spelling of the identifier fed into the inline Fprintf
// ("nlTotal", never "nlScopedTotal"), which cannot catch `nlTotal :=
// nlScopedTotal` -- a local rebind that keeps the fed identifier's NAME
// "nlTotal" while giving it the scoped stratum's VALUE.
//
// #1218's OWN fix -- factoring the line into corpusGrowthDriftLine and
// pointing this AST test and a new value test at it -- turned out to have
// the identical blind spot one level up: PR #1219 review ran the exact named
// mutation (`nlTotal = nlScopedTotal` immediately before the call) and both
// of #1218's tests passed, because neither one ever read main()'s own
// binding of nlTotal -- this AST test only checked the fed identifier's
// name (still "nlTotal" after the mutation), and the value test built its
// own nlTotal from scratch rather than driving main()'s.
//
// This fix pass removes the bare nlTotal int from this call site entirely
// (see corpusGrowthDriftValue's doc comment in main.go for the full
// argument and its stated residual gap): main() now calls
// corpusGrowthDriftLine(corpusGrowthDriftValue(nlUnscopedResults)), so this
// test's job narrows to what an AST test can actually prove -- that the
// call is present (deletion coverage) and that its argument is the raw
// unscoped results, not the scoped ones. The actual arithmetic --
// excluding UnscopedExempt cases from the total -- is now value-tested
// directly against corpusGrowthDriftValue in main_test.go's
// TestCorpusGrowthDriftValueExcludesUnscopedExemptCases, the same function
// this call site invokes, not a reimplementation of it.
func TestMainCallsCorpusGrowthDriftLineWithNlUnscopedResults(t *testing.T) {
	stmts := mainFuncBody(t)

	for _, stmt := range stmts {
		expr, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		outer, ok := expr.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := outer.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "fmt" || sel.Sel.Name != "Fprintln" {
			continue
		}
		if len(outer.Args) < 2 {
			continue
		}
		inner, ok := outer.Args[1].(*ast.CallExpr)
		if !ok {
			continue
		}
		fn, ok := inner.Fun.(*ast.Ident)
		if !ok || fn.Name != "corpusGrowthDriftLine" {
			continue
		}

		// Found the call. Its one argument must itself be a call to
		// corpusGrowthDriftValue, fed the raw unscoped results -- never
		// nlTotal, nlScopedTotal or nlScopedResults directly.
		if len(inner.Args) != 1 {
			t.Fatalf("corpusGrowthDriftLine call has %d argument(s), want exactly 1 (corpusGrowthDriftValue(nlUnscopedResults))", len(inner.Args))
		}
		valueCall, ok := inner.Args[0].(*ast.CallExpr)
		if !ok {
			t.Fatalf("corpusGrowthDriftLine's argument is a %T, want a call to corpusGrowthDriftValue", inner.Args[0])
		}
		valueFn, ok := valueCall.Fun.(*ast.Ident)
		if !ok || valueFn.Name != "corpusGrowthDriftValue" {
			t.Fatalf("corpusGrowthDriftLine's argument calls %v, want corpusGrowthDriftValue", valueCall.Fun)
		}
		if len(valueCall.Args) != 1 {
			t.Fatalf("corpusGrowthDriftValue call has %d argument(s), want exactly 1 (nlUnscopedResults)", len(valueCall.Args))
		}
		ident, ok := valueCall.Args[0].(*ast.Ident)
		if !ok {
			t.Fatalf("corpusGrowthDriftValue's argument is a %T, want a plain *ast.Ident", valueCall.Args[0])
		}
		if ident.Name != "nlUnscopedResults" {
			t.Fatalf("corpusGrowthDriftValue is called with %q, want nlUnscopedResults -- nlScopedResults wrongly includes the UnscopedExempt cases checkExemptions requires to diverge from the unscoped line (agent-estate#1215, agent-estate#1218)", ident.Name)
		}
		return
	}
	t.Fatal("main() no longer calls corpusGrowthDriftLine(corpusGrowthDriftValue(nlUnscopedResults)) via fmt.Fprintln(w, ...) -- test could not locate the agent-estate#1112 disclosure line it guards")
}
