// agent-tui is a left-anchored navigation rail over agent-supervisor's lane
// state. It reads exactly one surface -- the supervisor's "lanes" MCP tool,
// backed by lanes.sh --json -- and renders nothing tmux itself would not
// already show a human who ran that command. It never reads tmux directly
// and never writes to the ledger or dispatches anything: this is a viewer,
// same discipline as scripts/supervisor/laneview/ in agent-supervisor.
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
)

func main() {
	var (
		supervisorRepo = flag.String("supervisor-repo", os.Getenv("AGENT_SUPERVISOR_REPO"),
			"path to an agent-supervisor checkout (contains scripts/supervisor/mcp_server.py). "+
				"Defaults to $AGENT_SUPERVISOR_REPO.")
		python  = flag.String("python", envOr("AGENT_TUI_PYTHON", "python3"), "python interpreter to run mcp_server.py with")
		session = flag.String("session", os.Getenv("AGENT_SUPERVISOR_SESSION"), "tmux session to inspect; empty uses the supervisor's default")
		mcpCmd  = flag.String("mcp-cmd", os.Getenv("AGENT_TUI_MCP_CMD"),
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
	)
	flag.Parse()

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
		p := tea.NewProgram(cost.New(costFetch), tea.WithAltScreen())
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
		p := tea.NewProgram(gallery.New(), tea.WithAltScreen())
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

	var p *tea.Program
	if *showBoard {
		boardFetch := buildBoardFetch(*ledger, *ghBin, *sqliteBin, *repositories, lanesFetch)
		p = tea.NewProgram(board.New(boardFetch), tea.WithAltScreen())
	} else {
		// NewWithCost, not New: the rail is where agent-tui#4's "glanceable,
		// always there, no command to run" panel actually has to live --
		// see internal/rail.Model's costFetch doc comment.
		p = tea.NewProgram(rail.NewWithCost(lanesFetch, costFetch), tea.WithAltScreen())
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
