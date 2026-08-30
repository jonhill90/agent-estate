package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/jonhill90/agent-estate/src/tui/internal/apidocs"
	"github.com/jonhill90/agent-estate/src/tui/internal/external"
)

// openAPIRelPath is where hill90-app keeps the document API Docs renders,
// relative to that repo's root. Tracked in git there (services/api/dist/
// openapi/openapi.yaml is build output and is NOT tracked, which is why
// this points at src/ and not dist/).
const openAPIRelPath = "services/api/src/openapi/openapi.yaml"

// resolveOpenAPISpec turns the -openapi flag and $HILL90_APP_REPO into a
// readable path, or "" when neither yields one. Empty is a real, expected
// state -- this TUI must run with no hill90-app checkout at all (the same
// standalone requirement AGENTS.md states for $AGENT_SUPERVISOR_REPO) --
// and internal/apidocs' own view turns it into a notice naming this file
// rather than an empty table.
//
// An explicit -openapi that does not exist is NOT silently downgraded to
// "": it is returned as given, so the pane reports the path it could not
// read. A wrong path the user typed and a path they never set are
// different mistakes and get different messages.
func resolveOpenAPISpec(flagPath, appRepo string) string {
	if flagPath != "" {
		return flagPath
	}
	if appRepo == "" {
		return ""
	}
	candidate := filepath.Join(appRepo, openAPIRelPath)
	if _, err := os.Stat(candidate); err != nil {
		// $HILL90_APP_REPO is set but does not contain the document. Return
		// it anyway for the same reason as above: "I pointed at a repo and
		// the spec is not where it should be" is worth saying out loud.
		return candidate
	}
	return candidate
}

// buildAPIDocsFetch returns nil -- apidocs.New's own "unconfigured" state,
// which renders the notice naming what to set -- when no spec path was
// resolvable at all.
func buildAPIDocsFetch(specPath string) apidocs.Fetcher {
	if specPath == "" {
		return nil
	}
	return apidocs.NewFetcher(specPath)
}

// browserOpener is internal/external's Opener seam: the ONE place in this
// module that hands a URL to the host. It lives in cmd/ rather than under
// internal/ because AGENTS.md's adapter discipline puts every os/exec call
// at this layer -- internal/external knows only a func(string) error.
//
// A failed open is returned, never swallowed: the pane prints it. Nothing
// here parses or rewrites the URL; it is passed through exactly as
// internal/nav's tree carries it, so this cannot turn a nav entry into a
// different destination than the one on screen.
func browserOpener() external.Opener {
	return func(url string) error {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("%s: %w", cmd.Path, err)
		}
		// Start, not Run: a browser launcher on some desktops stays in the
		// foreground for the life of the browser, and waiting on it would
		// hang the TUI's own message loop behind it.
		go func() { _ = cmd.Wait() }()
		return nil
	}
}
