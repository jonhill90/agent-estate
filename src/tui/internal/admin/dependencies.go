package admin

// fetchDependencies checks each name in names against lookPath -- true
// when found on $PATH, false otherwise, mirroring
// internal/mcpservers.WithReachability's identical stdio-server check
// exactly (real os/exec.LookPath result, never fabricated). Unlike that
// function this never leaves an entry nil: every name in KnownDependencies
// gets a real answer, because (unlike an MCP server's http/sse transport,
// which WithReachability deliberately never probes) every dependency here
// is a local $PATH lookup, not a network call -- there is no "too
// expensive to check" case for this list.
func fetchDependencies(names []string, lookPath LookPath) ([]Dependency, error) {
	out := make([]Dependency, 0, len(names))
	for _, name := range names {
		_, err := lookPath(name)
		reachable := err == nil
		out = append(out, Dependency{Name: name, Reachable: &reachable})
	}
	return out, nil
}

// keychainRowName is the Dependency row name the keychain probe renders
// under -- agent-tui#149's proposal names the actual service checked
// ("Claude Code-credentials", the same item every lane authenticates
// against) so the row reads as a specific, checkable claim rather than a
// vague "keychain".
const keychainRowName = "keychain (Claude Code-credentials)"

// fetchKeychain turns one KeychainRunner call into a Dependency row,
// unchanged in shape from every other row fetchDependencies produces --
// agent-tui#149's own "Dependency already carries Reachable *bool with
// nil meaning unknown... fits that type unchanged" design point. probe's
// error is deliberately never rendered (never even inspected beyond
// nil-ness): a keychain probe's error text can itself be diagnostic
// information about what is or is not in the keychain, so the row states
// only reachability, matching this issue's own "do not leak the
// credential or its contents into the pane, logs, or an error string."
func fetchKeychain(probe KeychainRunner) Dependency {
	readable, err := probe()
	if err != nil {
		// The probe itself could not produce an answer (timeout, could
		// not start) -- "could not determine," never collapsed onto
		// "locked-or-denied" (Reachable stays nil, the same "we did not
		// get an answer" state every other nil Reachable in this package
		// already means).
		return Dependency{Name: keychainRowName}
	}
	return Dependency{Name: keychainRowName, Reachable: &readable}
}
