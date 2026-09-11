package knowledge

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// disqualifiedImports is exactly the three import paths agent-estate#1378's
// review named: "os/exec" (any subprocess), "net" (a raw dial), "net/http"
// (an HTTP client) -- the two structural properties skillsSource's own doc
// comment claims ("no *exec.Cmd, no network call... appears anywhere in
// its call graph"). Deliberately NOT every net/* package: net/url parses a
// string and net/mail parses an address -- neither makes a call on its
// own, and banning them would fail this check against legitimate, harmless
// future uses with zero security relevance to what this check exists to
// catch.
var disqualifiedImports = map[string]bool{
	"os/exec":  true,
	"net":      true,
	"net/http": true,
}

// packageCallGraph is a lightweight, same-package call graph over
// internal/knowledge's own non-test .go files -- go/ast, the same tool
// classify_test.go's own classifiedSourceNames (that file's AST walk over
// classify.go's switch) already uses elsewhere in this package, not a new
// dependency. It answers one question: starting from a named function,
// which OTHER package-level functions does it call (directly or
// transitively), and does any function in that reachable set directly
// call something from disqualifiedImports?
//
// SCOPE, ARGUED (agent-estate#1378's review asked this be decided and
// justified, not defaulted). This walks same-package Go function calls
// only -- bare-identifier calls to another func declared in this same
// package, and qualified pkg.Func() calls resolved through each call
// SITE's own file-level import aliases. It does NOT follow:
//
//   - calls through a function-typed value (a closure, an interface
//     method, an injected callback parameter/field) -- skillsSource takes
//     only a repoRoot string today and has no such seam at all (the PR's
//     own argument, independently confirmed by the review). If a future
//     change adds one, that is itself a visible, reviewable structural
//     change (a new Config field, a new parameter on skillsSource's own
//     signature) -- not a silent one this check would need to catch
//     invisibly, and exactly the kind of change a reviewer re-reading
//     this doc comment's own claim would be looking for anyway.
//   - calls into other agent-estate packages -- skills.go imports none.
//   - method calls on a receiver -- no function in this package's own
//     call graph reachable from skillsSource today is a method; adding
//     one is, again, a visible signature change.
//   - stdlib functions other than the three named above -- os.Open,
//     path/filepath.Join, encoding/json.Unmarshal etc. are not what "no
//     exec, no network" means, and banning them would make this an
//     unusable, ever-growing exemption list, the exact rot
//     docs/ci-rules-retired.md already has scars from.
//
// A FILE-LEVEL check (does skills.go itself import os/exec) was
// considered and rejected: skillsSource already calls hashtag/truncate
// (defined in stars.go) and classify (classify.go) and itemID (id.go).
// stars.go legitimately imports os/exec, for defaultGHRunner --
// github-stars is a real, necessary shell-out. A file-level import check
// on stars.go would therefore either false-positive on every legitimate
// github-stars call (stars.go imports os/exec, full stop) or need an
// exemption list; a check scoped to skills.go ALONE would pass today but
// miss a future change where skillsSource starts calling a helper
// DEFINED elsewhere that itself shells out -- exactly the gap the review
// named. Walking the actual call graph and checking each reached
// FUNCTION's own body (never its file) is the only shape that is both
// correctly permissive today -- it passes hashtag/truncate/classify/
// itemID, none of which call exec or net in their own bodies, confirmed
// by this same test passing against the unmodified package -- and still
// catches the shape of gap this exists for.
type packageCallGraph struct {
	funcs map[string]*ast.FuncDecl // package-level func name -> declaration
	files map[string]*ast.File     // package-level func name -> its own containing file (for import resolution)
}

// loadKnowledgePackageCallGraph parses every non-test .go file in the
// current directory (this package, when run via `go test`) and indexes
// every package-level function declaration. Test files are excluded on
// purpose: a test helper calling exec/net (there are none today) says
// nothing about skillsSource's own PRODUCTION reachability, and including
// them would let a test fixture's own unrelated shell-out fail this check
// for a reason that has nothing to do with the property being pinned.
func loadKnowledgePackageCallGraph(t *testing.T) *packageCallGraph {
	t.Helper()
	fset := token.NewFileSet()
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing package files: %v", err)
	}
	g := &packageCallGraph{funcs: map[string]*ast.FuncDecl{}, files: map[string]*ast.File{}}
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil { // package-level funcs only -- see this type's own doc comment on method calls
				continue
			}
			g.funcs[fn.Name.Name] = fn
			g.files[fn.Name.Name] = file
		}
	}
	if len(g.funcs) == 0 {
		t.Fatal("loadKnowledgePackageCallGraph found zero package-level functions -- parser is broken or this package's shape changed under it")
	}
	return g
}

// importAlias resolves how ident is written at a call site in file back to
// the real import path -- usually a package's own last path component
// (`exec` for `"os/exec"`), but an explicit alias overrides it. This is
// what lets a call written as `exec.Command(...)` resolve to "os/exec"
// even though the identifier at the call site is just "exec".
func importAlias(file *ast.File, ident string) (path string, ok bool) {
	for _, imp := range file.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		name := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == ident {
			return p, true
		}
	}
	return "", false
}

// disqualifiedCallsReachableFrom walks the call graph starting at
// startFunc (breadth-first over same-package calls, cycle-safe via
// visited) and returns one human-readable description per direct call,
// anywhere in the reachable set, into a disqualifiedImports package --
// nil if none. Each entry names the calling function and the exact
// qualified call, so a real failure is immediately actionable, not just
// "something, somewhere".
func (g *packageCallGraph) disqualifiedCallsReachableFrom(startFunc string) []string {
	var found []string
	visited := map[string]bool{}
	var walk func(name string)
	walk = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		fn, ok := g.funcs[name]
		if !ok || fn.Body == nil {
			return
		}
		file := g.files[name]
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				// A bare-identifier call -- same-package function. Recurse
				// into it so its own body is checked too, and so ITS calls
				// expand the reachable set further.
				if _, isFunc := g.funcs[fun.Name]; isFunc {
					walk(fun.Name)
				}
			case *ast.SelectorExpr:
				// pkg.Func(...) -- resolve pkg through this call SITE's own
				// file (imports are per-file in Go, not per-package), not
				// the file skillsSource itself lives in.
				pkgIdent, ok := fun.X.(*ast.Ident)
				if !ok {
					return true
				}
				if path, ok := importAlias(file, pkgIdent.Name); ok && disqualifiedImports[path] {
					found = append(found, name+" calls "+path+"."+fun.Sel.Name+"()")
				}
			}
			return true
		})
	}
	walk(startFunc)
	return found
}

// TestSkillsSourceCallGraphNeverReachesExecOrNet mechanically pins the
// half of skillsSource's own doc comment claim that
// TestSkillsSourceNeverInstallsAnything (skills_test.go) does not:
// TestSkillsSourceNeverInstallsAnything observes the filesystem
// before/after a real call and pins "no write"; this test statically pins
// "no *exec.Cmd, no network call... appears anywhere in its call graph"
// by construction, rather than by observing one run's side effects (a
// call that only shells out on an error path, or under an argument this
// test's own fixtures never exercise, could exist without ever tripping a
// before/after filesystem snapshot -- a static check has no such blind
// spot for THIS property, because it examines every reachable function's
// source, not one execution's behavior).
//
// agent-estate#1378's review named the exact mutation this must catch:
// injecting `exec.Command("true").Run()` into skillsSource passed every
// existing test in the package, including TestSkillsSourceNeverInstalls
// Anything. See the PR body for both mutation outcomes (exec and,
// separately, an http.Get call) pasted against this test specifically.
func TestSkillsSourceCallGraphNeverReachesExecOrNet(t *testing.T) {
	g := loadKnowledgePackageCallGraph(t)
	if _, ok := g.funcs["skillsSource"]; !ok {
		t.Fatal("skillsSource not found in this package's own call graph -- test setup is broken, not a pass")
	}
	if found := g.disqualifiedCallsReachableFrom("skillsSource"); len(found) > 0 {
		t.Fatalf("skillsSource's call graph reaches os/exec, net, or net/http -- the doc comment's own inertness claim no longer holds:\n  %s",
			strings.Join(found, "\n  "))
	}
}

// TestCallGraphCheckPassesTodayForKnownSafeHelpers pins the check's own
// permissiveness, not just its bite: skillsSource genuinely calls
// hashtag/truncate (stars.go, the SAME FILE as defaultGHRunner, which DOES
// import os/exec) and classify (classify.go) and itemID (id.go) today.
// This asserts the call graph walk reaches into stars.go (proving it is
// not scoped to skills.go alone, the file-level check this type's own doc
// comment argues against) and still finds nothing disqualified there --
// the false-positive risk a file-level or package-level import check
// would not have avoided.
func TestCallGraphCheckPassesTodayForKnownSafeHelpers(t *testing.T) {
	g := loadKnowledgePackageCallGraph(t)
	for _, name := range []string{"hashtag", "truncate", "classify", "itemID"} {
		if _, ok := g.funcs[name]; !ok {
			t.Fatalf("expected helper %s not found -- this test's own premise (skillsSource calls it) no longer holds", name)
		}
	}
	if found := g.disqualifiedCallsReachableFrom("skillsSource"); len(found) > 0 {
		t.Fatalf("expected zero disqualified calls reachable from skillsSource today, got: %v", found)
	}
	// Confirm the walk actually REACHED stars.go's own functions, rather
	// than vacuously passing because it never got there -- a check that
	// cannot see the file it is supposed to be more permissive than would
	// pass this same assertion for the wrong reason.
	visited := map[string]bool{}
	var walk func(name string)
	walk = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		fn, ok := g.funcs[name]
		if !ok || fn.Body == nil {
			return
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if _, isFunc := g.funcs[id.Name]; isFunc {
					walk(id.Name)
				}
			}
			return true
		})
	}
	walk("skillsSource")
	if !visited["hashtag"] || !visited["truncate"] {
		t.Fatal("call graph walk from skillsSource never reached hashtag/truncate -- this test's own premise is broken, the permissiveness claim above is unproven")
	}
}
