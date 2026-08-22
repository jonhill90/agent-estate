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
