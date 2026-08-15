// agent-tui is a left-anchored navigation rail over agent-supervisor's lane
// state. It reads the supervisor's MCP surface -- "sessions" (agent-tui#13,
// every tmux session grouped) for the rail, "lanes" (one session) for
// -board -- backed by sessions.sh/lanes.sh --json, and renders nothing
// tmux itself would not already show a human who ran those commands. It
// never reads tmux directly and never writes to the ledger or dispatches
// anything: this is a viewer, same discipline as
// scripts/supervisor/laneview/ in agent-supervisor.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/board"
	"github.com/jonhill90/agent-tui/internal/cost"
	"github.com/jonhill90/agent-tui/internal/gallery"
	"github.com/jonhill90/agent-tui/internal/lane"
	"github.com/jonhill90/agent-tui/internal/mcp"
	"github.com/jonhill90/agent-tui/internal/rail"
	"github.com/jonhill90/agent-tui/internal/theme"
	// sessionops, not session: the -session flag var below already owns that
	// name (the tmux session -board reads), and shadowing it would be an
	// easy read-vs-write mixup in a file that now does both.
	sessionops "github.com/jonhill90/agent-tui/internal/session"
)

func main() {
	var (
		supervisorRepo = flag.String("supervisor-repo", os.Getenv("AGENT_SUPERVISOR_REPO"),
			"path to an agent-supervisor checkout (contains scripts/supervisor/mcp_server.py). "+
				"Defaults to $AGENT_SUPERVISOR_REPO.")
		python  = flag.String("python", envOr("AGENT_TUI_PYTHON", "python3"), "python interpreter to run mcp_server.py with")
		session = flag.String("session", os.Getenv("AGENT_SUPERVISOR_SESSION"), "tmux session to inspect; empty uses the supervisor's default. "+
			"Only affects -board's single-session lane read -- the rail (agent-tui#13) reads every session via the "+
			"supervisor's \"sessions\" tool and ignores this flag.")
		directorSession = flag.String("director-session", envOr("AGENT_TUI_DIRECTOR_SESSION", "director"),
			"tmux session name styled distinctly in the rail (agent-tui#13 -- \"it should look a bit different, "+
				"something to make it special\"). Set to \"\" to disable the distinct styling entirely rather than "+
				"have it silently match nothing.")
		mcpCmd = flag.String("mcp-cmd", os.Getenv("AGENT_TUI_MCP_CMD"),
			"full override command line for the MCP server, e.g. a remote SSH hop. Takes precedence over -supervisor-repo.")
		showBoard = flag.Bool("board", false, "show the task board (agent-tui#6) instead of the rail -- a separate screen, "+
			"never a rail restyle. Read-only: derives its columns fresh on every fetch, never stores one.")
		ledger = flag.String("ledger", envOr("AGENT_TUI_LEDGER", defaultLedgerPath()),
			"path to a ledger.sqlite3 to read for the board -- must point at a COPY, never the live "+
				"supervisor's own ledger (agent-tui#6's rule). There is no default: unlike -supervisor-repo, "+
				"this never falls back to the live state dir, because the board's ledger read (`sqlite3 "+
				"PRAGMA query_only=1`, not `-readonly` -- see internal/board/ledger.go) would otherwise be "+
				"free to open the live ledger and write `-wal`/`-shm` sidecars next to it. Set $AGENT_TUI_LEDGER "+
				"or pass -ledger explicitly, pointed at a copy.")
		ghBin        = flag.String("gh-bin", envOr("AGENT_GH_BIN", "gh"), "gh binary for the board's issue/PR reads")
		sqliteBin    = flag.String("sqlite-bin", envOr("AGENT_SQLITE_BIN", "sqlite3"), "sqlite3 binary for the board's ledger reads")
		repositories = flag.String("repositories", os.Getenv("SUPERVISOR_REPOSITORIES"),
			"colon-separated name=path=owner/repo entries, same shape as agent-supervisor's SUPERVISOR_REPOSITORIES "+
				"(.env.example) -- the board unions this with every repo it discovers in the ledger's own source_urls, "+
				"so it never has to hardcode a list. Defaults to $SUPERVISOR_REPOSITORIES.")
		showCost = flag.Bool("cost", false, "show the cost panel (agent-tui#4) instead of the rail -- a separate "+
			"screen, never a rail restyle. Reads ccusage, never reimplements its per-harness usage parse.")
		ccusageBin = flag.String("ccusage-bin", envOr("AGENT_TUI_CCUSAGE_BIN", "npx"),
			"binary to run for ccusage calls. Combined with -ccusage-args (default \"ccusage\"). Point this at a "+
				"binary that does not exist to exercise the blindness path (agent-tui#4 acceptance item 2): the "+
				"panel must render \"unknown\", never 0.")
		ccusageArgs = flag.String("ccusage-args", envOr("AGENT_TUI_CCUSAGE_ARGS", "ccusage"),
			"space-separated args prepended before every ccusage subcommand, e.g. \"ccusage\" for `npx ccusage ...` "+
				"or empty if -ccusage-bin is already the ccusage binary itself.")
		claudeBlockLimit = flag.Int64("claude-block-limit", envOrInt64("AGENT_TUI_CLAUDE_BLOCK_LIMIT", 0),
			"token ceiling for Claude's active 5h session block, passed to `ccusage blocks --token-limit`. ccusage "+
				"has no default of its own (it cannot know your plan's real cap) -- leave unset and the panel shows "+
				"Claude's limit as \"unknown\", never a fabricated percentage (agent-tui#4's honesty constraint).")
		showGallery = flag.Bool("gallery", false, "show the glyph gallery (agent-tui#11) instead of the rail -- every "+
			"lane state against every candidate glyph, including glyphs not yet in any set, each flagged with "+
			"whether it needs a Nerd Font. A separate screen, never a rail restyle; reads no lanes and needs no "+
			"supervisor connection, same as -cost.")
		boardRefresh = flag.Duration("board-refresh", envOrDuration("AGENT_TUI_BOARD_REFRESH", board.DefaultRefreshInterval),
			"how often -board re-fetches on its own tick (agent-tui#28). The previous hardcoded 5s measured at "+
				"~8,160 GitHub GraphQL points/hr against gh issue list/gh pr list -- against a shared 5,000/hr "+
				"budget, enough to starve agent-supervisor's own dispatch (agent-supervisor#144). This is a "+
				"screen a human reads, not a control loop; [r] still refreshes on demand. Defaults to "+
				"$AGENT_TUI_BOARD_REFRESH.")
	)
	flag.Parse()

	// agent-tui#27: the one place a user's theme preference is resolved.
	// theme.Load's three outcomes (missing/malformed/unknown -- see its own
	// doc comment) are honored exactly as it returns them: a missing config
	// renders as today (activeTheme falls back to theme.Default with an
	// empty notice), while a malformed config or an unknown theme name
	// still resolves to theme.Default but carries a notice every screen
	// below renders visibly -- #27 acceptance item 3, "an undeterminable
	// preference is never silently treated as a valid one." Every screen
	// (rail, board, cost, gallery) gets the SAME theme -- swapping it is
	// editing this one Load call or the user's config file, never a
	// per-screen setting.
	activeTheme, themeNotice := theme.Load(theme.ConfigPath())

	if *showBoard && *ledger == "" {
		fmt.Fprintln(os.Stderr, "agent-tui: -board needs -ledger (or $AGENT_TUI_LEDGER) pointed at a COPY of "+
			"the ledger; refusing to default to the live supervisor ledger (agent-tui#6's rule -- see -ledger's "+
			"flag help)")
		os.Exit(1)
	}

	// Built once, used by both the -cost detail screen and the rail's
	// default cost line below -- the two are separate consumers of the
	// same Fetcher, not separate cost implementations.
	costFetch := buildCostFetch(*ccusageBin, splitArgs(*ccusageArgs), *claudeBlockLimit, time.Now)

	// The cost panel reads only ccusage -- never lanes, never the
	// supervisor MCP server -- so it is the one screen that must not pay
	// (or require) a supervisor connection to start. Every other screen
	// (rail, board) needs lanes and connects exactly as before.
	if *showCost {
		p := tea.NewProgram(cost.New(costFetch).WithTheme(activeTheme, themeNotice), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "agent-tui:", err)
			os.Exit(1)
		}
		return
	}

	// The gallery reads compiled-in glyph data (lane.Variants,
	// lane.Candidates) and nothing else -- no lanes, no ccusage, no
	// supervisor connection, same reasoning as -cost above.
	if *showGallery {
		p := tea.NewProgram(gallery.New().WithTheme(activeTheme, themeNotice), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "agent-tui:", err)
			os.Exit(1)
		}
		return
	}

	client, cleanup, err := connect(*supervisorRepo, *python, *mcpCmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-tui:", err)
		os.Exit(1)
	}
	defer cleanup()

	lanesFetch := func() ([]lane.Lane, error) {
		args := map[string]any{}
		if *session != "" {
			args["session"] = *session
		}
		text, err := client.CallTool("lanes", args)
		if err != nil {
			return nil, err
		}
		return lane.Decode(text)
	}

	// sessionsFetch is agent-tui#13's fix for the actual regression: "lanes"
	// (above) is single-session BY CONSTRUCTION -- see agent-supervisor's
	// lanes.sh header -- so the rail now reads "sessions" instead, which
	// answers for every tmux session sessions.sh can see. This program
	// still never enumerates or shells out to tmux itself (agent-tui#14):
	// it calls one more MCP tool, nothing more.
	sessionsFetch := func() ([]lane.Session, error) {
		text, err := client.CallTool("sessions", map[string]any{})
		if err != nil {
			return nil, err
		}
		return lane.DecodeSessions(text)
	}

	var p *tea.Program
	if *showBoard {
		boardFetch := buildBoardFetch(*ledger, *ghBin, *sqliteBin, *repositories, lanesFetch)
		p = tea.NewProgram(
			board.NewWithRefreshInterval(boardFetch, *boardRefresh).WithTheme(activeTheme, themeNotice),
			tea.WithAltScreen(),
		)
	} else {
		// NewMultiSession, not NewWithCost: agent-tui#13 is the regression
		// that shipped a rail showing one session's lanes when six sessions
		// (including `director`) exist. See internal/rail.Model's
		// sessionsFetch doc comment for why this is additive rather than a
		// change to what NewWithCost itself does (board.go's single-session
		// read is untouched). lanesFetch is at#18's fallback: agent-tui#13
		// alone had no way to render anything at all if the supervisor's
		// "sessions" tool (agent-supervisor#158) was not yet available --
		// the reviewer reproduced exactly that against a real, un-patched
		// supervisor checkout. Passing lanesFetch here means that case
		// degrades to the old single-session rail (with a visible note)
		// instead of going blind.
		// WithOps wires agent-tui#14's write path (attach/detach/add/remove)
		// in on top of #13's multi-session rail -- session.New(client) shares
		// the exact same MCP connection every read above already uses, never
		// a second client and never tmux itself (see internal/session's
		// package doc for why that split is non-negotiable).
		m := rail.NewMultiSession(sessionsFetch, lanesFetch, costFetch, *directorSession).
			WithOps(sessionops.New(client)).
			WithTheme(activeTheme, themeNotice)
		p = tea.NewProgram(m, tea.WithAltScreen())
	}
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-tui:", err)
		os.Exit(1)
	}
}

func connect(supervisorRepo, python, mcpCmd string) (*mcp.Client, func(), error) {
	var c *mcp.Client
	var err error

	switch {
	case mcpCmd != "":
		c, err = mcp.Start("sh", "-c", mcpCmd)
	case supervisorRepo != "":
		script := supervisorRepo + "/scripts/supervisor/mcp_server.py"
		if _, statErr := os.Stat(script); statErr != nil {
			return nil, nil, fmt.Errorf("mcp_server.py not found at %s (pass -supervisor-repo or -mcp-cmd)", script)
		}
		c, err = mcp.Start(python, script)
	default:
		return nil, nil, fmt.Errorf("no supervisor to connect to: set -supervisor-repo, $AGENT_SUPERVISOR_REPO, or -mcp-cmd")
	}
	if err != nil {
		return nil, nil, err
	}
	return c, func() { _ = c.Close() }, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envOrDuration parses $key as a Go duration string (e.g. "60s", "5m"); an
// unset or unparsable value falls back rather than erroring, same
// leniency envOrInt64 already gives -claude-block-limit.
func envOrDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func envOrInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// splitArgs splits -ccusage-args on whitespace, dropping empty fields --
// "" (the empty flag value) must become no args, not one empty-string arg
// that exec would pass straight through to the ccusage binary.
func splitArgs(s string) []string {
	fields := strings.Fields(s)
	return fields
}
