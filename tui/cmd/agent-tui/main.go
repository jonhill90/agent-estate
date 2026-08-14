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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/board"
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
	)
	flag.Parse()

	if *showBoard && *ledger == "" {
		fmt.Fprintln(os.Stderr, "agent-tui: -board needs -ledger (or $AGENT_TUI_LEDGER) pointed at a COPY of "+
			"the ledger; refusing to default to the live supervisor ledger (agent-tui#6's rule -- see -ledger's "+
			"flag help)")
		os.Exit(1)
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
		p = tea.NewProgram(rail.New(lanesFetch), tea.WithAltScreen())
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
