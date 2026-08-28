# Docs group -- rows 18 and 19 of testdata/vhs/full-nav-walk-report.md,
# the two destinations agent-tui#94's walk recorded as STUB and this
# change replaces with real behaviour.
#
# This tape drives ONLY the Docs pair. The full walk
# (testdata/vhs/full-nav-walk.tape) re-measures all 24 destinations and is
# what the other rows' verdicts come from; re-running it here would
# re-state verdicts this change did not re-earn, which is how a stale
# verdict spreads (this file's own report says so).
#
# Navigation, same mechanics the full walk documents: selecting a route
# leaves focus on the SIDEBAR, so this is one continuous downward walk.
# Down x11 reaches the Docs group header (Home, Dashboard, Agents, Chat,
# Tasks, Knowledge, Library, Lanes, then the Build/Connect/Observe headers
# -- every group stays collapsed, so their children are skipped), Right
# expands it, and the next Down lands on API Docs.
#
# $HILL90_APP_REPO is exported so API Docs has its real source to read.
# Without it the pane says which document would back the route and how to
# point at it, rather than falling back to a placeholder -- 18c below
# captures that state by relaunching with the variable unset.
Output testdata/vhs/out/docs.gif
Set FontSize 13
Set Width 1600
Set Height 900
Set Shell "bash"

Hide
# Derived, not hardcoded: this tape lives inside the repo it points at
# (agent-estate#754 -- the prior $HOME/.../agent-supervisor literal went
# stale the day the repo was renamed to agent-estate, and every hardcoded
# host path recurs the same way on the next rename). git rev-parse falls
# back to pwd only if run outside a git checkout.
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}" Enter
Type "export HILL90_APP_REPO=${HILL90_APP_REPO:-$HOME/source/repos/Personal/hill90-app}" Enter
Type "go build -o /tmp/atui-docs ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-docs" Enter
Sleep 3s

# --- Docs group: API Docs (row 18), Platform Docs (row 19) ---

Down Down Down Down Down Down Down Down Down Down Down
Right
Sleep 300ms
Down
Enter
Sleep 3s
Screenshot testdata/vhs/out/18-docs-api-docs.png

# The same pane, filtered: [tab] moves focus off the sidebar and into the
# content pane, then "/" filters the operation table by path.
Tab
Type "/"
Type "agents/{id}"
Enter
Sleep 2s
Screenshot testdata/vhs/out/18b-docs-api-docs-filtered.png

# Back to the sidebar for the second destination.
Tab
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/19-docs-platform-docs.png

# --- The same route with NO source available ---
# Quit and relaunch with HILL90_APP_REPO unset: API Docs must name the
# document it would read and how to point at it. "No source configured" is
# a different statement from "not built yet", and this is the capture that
# shows which one this pane makes.
Type "q"
Sleep 1s
Hide
Type "unset HILL90_APP_REPO && clear" Enter
Sleep 1s
Show
Type "/tmp/atui-docs" Enter
Sleep 3s
Down Down Down Down Down Down Down Down Down Down Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/18c-docs-api-docs-unconfigured.png

Sleep 1s
