// Package livepath is the ONE live-corpus-path guard every command that
// might write to a SQLite copy of ~/corpus/corpus.sqlite3 (renamed from
// ledger.sqlite3, agent-estate#P6; the old name is kept as a compat
// symlink -- this package's identity-based comparison, see
// RefuseLivePath's own doc comment, resolves either name to the same
// file without needing to know both spellings) shares. It was
// factored out of cmd/provenancebackfill (agent-estate#1139) so a second
// ingestion command (cmd/codexingest) reuses the exact same refusal rather
// than forking a second copy of it -- the task brief that created
// cmd/codexingest is explicit: "Reuse the merged live-path guard ... rather
// than writing a second one."
//
// This package answers exactly one question -- does a candidate path,
// however it is spelled or reached, lead to the SAME FILE as the live
// corpus? -- and refuses (rather than guesses) whenever that question cannot
// be answered with certainty. See RefuseLivePath's own doc comment for the
// three-revision history of why string comparison alone was never enough.
package livepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
)

// RefuseLivePath reports whether dbPath names the live corpus.
//
// This is the third revision of this guard (see cmd/provenancebackfill's git
// history for the first two, each of which shipped a defect of the same
// shape: comparing PATH STRINGS, then adding a special case each time a
// reviewer found a shape whose string didn't match but whose target did --
// agent-estate#1139, PR #1232's two reviews, then PR #1233's review). Three
// rounds of "handle this shape too" is what happens when the wrong question
// is asked -- "does this string look like the live path" can never be
// complete, because a filesystem has more ways to name one file than any
// string comparison enumerates (a symlink at any depth, a case-varied
// spelling, a hardlink under an unrelated name...).
//
// This revision asks a different question: does dbPath, however it is
// spelled or reached, lead to the SAME FILE as internal/corpus.Path()? Two
// mechanisms answer that, and BOTH must actively clear a path before it is
// permitted -- uncertainty from either one refuses:
//
//  1. ResolveForCompare resolves dbPath as far as the filesystem will allow,
//     component by component, including every parent and grandparent
//     directory -- not just the leaf. If ANY existing component is a
//     symlink that cannot be resolved (dangling target, ELOOP, or an
//     Lstat failure that isn't plain "doesn't exist yet" -- permission
//     denied, for instance), resolution stops with an error and
//     RefuseLivePath refuses outright. It never falls back to comparing
//     what's left of the string in that case.
//  2. Where the resolved candidate exists on disk, it is compared against
//     the resolved live path by os.SameFile (device+inode identity), not
//     by string equality. This is what a string comparison structurally
//     cannot do: a hardlink to the live file under an unrelated name in an
//     unrelated directory has no symlink for EvalSymlinks to follow and no
//     matching path string, but it IS the live file, and SameFile sees
//     that.
//
// The resolved, case-folded path string is kept as an ADDITIONAL refusal
// condition (it still catches the not-yet-existing-leaf case, where there is
// no inode yet for SameFile to compare) -- never as the sole basis to
// PERMIT. Permit requires resolution to have succeeded cleanly with no
// identity or string match found; any component that could not be resolved,
// or any stat that could not be performed, refuses instead of guessing.
//
// Known limitation, out of scope for this guard: TOCTOU. This function is
// called once by a caller before dbPath is reopened by path many times over
// (once per SQL statement, each a fresh `sqlite3` subprocess). A symlink
// swapped into place after this check and before one of those later opens
// would write through undetected. Closing that window means re-checking
// identity at every reopen (or holding an open, unretargetable file
// descriptor across the whole run) -- a structural change to how a caller
// opens the database, not a fix this guard can make on its own. Tracked, not
// attempted here.
func RefuseLivePath(dbPath string) (string, bool) {
	candidate, err := ResolveForCompare(dbPath)
	if err != nil {
		return fmt.Sprintf("cannot resolve %s: %v -- refusing rather than guessing whether it names the live corpus", dbPath, err), true
	}

	livePath, err := corpus.Path()
	if err != nil {
		// corpus.Path()'s only failure mode is os.UserHomeDir() erroring
		// (internal/corpus's dbPath(): $ESTATE_CORPUS if set, otherwise
		// os.UserHomeDir() or its error, nothing else). A prior revision of
		// this guard fell through here to a "$HOME-anchored" fallback that
		// called os.UserHomeDir() a SECOND time to decide how to anchor it --
		// but it is the same call, in the same process, against the same
		// environment: if it failed once it fails again, so that branch was
		// dead code in the exact scenario it existed to cover, and the
		// fallback silently degraded to a bare cwd-relative comparison that
		// doesn't match the real live path (agent-estate#1139, PR #1234
		// review). Refuse outright instead of guessing: an unresolvable live
		// reference is not evidence the candidate is safe.
		return fmt.Sprintf("cannot resolve the live corpus path: %v -- refusing rather than guessing whether %s names it", err, dbPath), true
	}
	live, liveErr := ResolveForCompare(livePath)
	if liveErr != nil {
		// The candidate side already refuses when ITS OWN resolution
		// fails; the live reference side must refuse identically when
		// IT cannot be resolved, rather than silently permitting with
		// nothing to compare against. An unresolvable live path is not
		// evidence the candidate is safe -- it is the one case where
		// identity cannot be established at all, and uncertainty here
		// refuses exactly as it does for the candidate.
		return fmt.Sprintf("cannot resolve live corpus path %s: %v -- refusing rather than guessing whether %s names it", livePath, liveErr, dbPath), true
	}
	if candidate.Info != nil && live.Info != nil && os.SameFile(candidate.Info, live.Info) {
		return fmt.Sprintf("is the same file as the live corpus (%s), by device+inode identity", livePath), true
	}
	if candidate.Clean == live.Clean {
		return fmt.Sprintf("matches the live corpus path (%s)", livePath), true
	}
	if strings.Contains(candidate.Clean, strings.ToLower("agent-dotfiles-supervisor")) {
		return "matches the retired agent-dotfiles-supervisor ledger location", true
	}
	return "", false
}

// ResolvedPath is what ResolveForCompare produces: a string form for the
// (necessarily incomplete) additional string-equality refusal, and, when the
// resolved path actually exists, the os.Stat result RefuseLivePath uses for
// identity comparison via os.SameFile. Info is nil exactly when nothing
// exists at the resolved path yet (e.g. a -db leaf that hasn't been created)
// -- callers must not treat a nil Info as a mismatch, only as "no identity
// signal available here."
type ResolvedPath struct {
	Clean string
	Info  os.FileInfo
}

// ResolveForCompare resolves p as far as the filesystem allows and reports
// both a comparable string form and, if the resolved path exists, its
// os.Stat identity. It expands a leading "~" against $HOME (flag.String
// never does), makes the result absolute, and case-folds the string form
// since this tool runs on case-insensitive filesystems (APFS default) where
// two differently-spelled strings can name the same file.
//
// Resolution walks the cleaned path one component at a time, from the root
// down, through resolveComponentsStrict -- see that function's doc comment
// for why every component (not just the leaf) must be checked, and why an
// unresolvable component anywhere in the chain returns an error rather than
// a partial answer.
func ResolveForCompare(p string) (ResolvedPath, error) {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			p = home
		}
	} else if strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	clean := filepath.Clean(abs)

	resolved, err := resolveComponentsStrict(clean)
	if err != nil {
		return ResolvedPath{}, err
	}

	var info os.FileInfo
	if fi, statErr := os.Stat(resolved); statErr == nil {
		info = fi
	}
	return ResolvedPath{Clean: strings.ToLower(resolved), Info: info}, nil
}

// resolveComponentsStrict walks clean (already absolute) one path component
// at a time, resolving every symlink it finds along the way -- a parent
// directory's symlink, a grandparent's, or the leaf's, all treated
// identically, unlike a single leaf-only os.Lstat.
//
// Three outcomes, and only one of them returns a usable path:
//
//   - A component does not exist (os.IsNotExist): nothing under it can
//     exist either, so nothing under it can be a symlink. The remaining
//     components are appended literally and the walk stops. This is the
//     ordinary "-db names a file that hasn't been created yet" case, and it
//     is the ONLY case where "doesn't fully resolve" is treated as safe to
//     continue with -- because there is provably nothing left to resolve.
//   - A component IS a symlink: filepath.EvalSymlinks must resolve it (to
//     however many hops that takes). Failure here -- a dangling target, an
//     ELOOP cycle -- returns an error immediately. This is what closes the
//     symlinked-parent-directory bypass PR #1233's review found: a parent
//     component being unresolvable is no longer silently skipped just
//     because the walk was checking the leaf.
//   - Any other Lstat failure on an existing-or-uncertain component
//     (permission denied, for example) also returns an error. Fail-closed
//     means an inspection that could not be performed is never treated as
//     evidence of safety.
func resolveComponentsStrict(clean string) (string, error) {
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean[len(volume):], string(filepath.Separator))
	if rest == "" {
		return clean, nil
	}
	parts := strings.Split(rest, string(filepath.Separator))

	resolved := volume + string(filepath.Separator)
	for i, part := range parts {
		if part == "" {
			continue
		}
		next := filepath.Join(resolved, part)
		info, err := os.Lstat(next)
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.Join(append([]string{resolved}, parts[i:]...)...), nil
			}
			return "", fmt.Errorf("cannot stat %s: %w", next, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(next)
			if err != nil {
				return "", fmt.Errorf("symlink %s does not resolve to an existing target: %w", next, err)
			}
			resolved = target
			continue
		}
		resolved = next
	}
	return resolved, nil
}
