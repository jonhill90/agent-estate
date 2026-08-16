// keelson is one application -- a persistent left-anchored navigation rail
// over agent-supervisor's lane state, with the task board, cost panel and
// glyph gallery reachable as panes in the same process (agent-tui#38; see
// internal/shell). Renamed from agent-tui, which named the rendering
// technology (Bubble Tea, "tui") rather than the product -- this is the
// layer above orchestration, binding the panes into one structure, the
// way a ship's keelson binds its frames to the keel.
//
// It reads the supervisor's MCP surface -- "sessions" (agent-tui#13, every
// tmux session grouped) for the rail, "lanes" (one session) for -board --
// backed by sessions.sh/lanes.sh --json, and renders nothing tmux itself
// would not already show a human who ran those commands. It never reads
// tmux directly and never writes to the ledger or dispatches anything:
// this is a viewer (plus the session write path, internal/session), same
// discipline as scripts/supervisor/laneview/ in agent-supervisor.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/cost"
	"github.com/jonhill90/keelson/internal/gallery"
	"github.com/jonhill90/keelson/internal/lane"
	"github.com/jonhill90/keelson/internal/mcp"
	"github.com/jonhill90/keelson/internal/rail"
	"github.com/jonhill90/keelson/internal/shell"
	"github.com/jonhill90/keelson/internal/theme"
	// sessionops, not session: the -session flag var below already owns that
	// name (the tmux session -board reads), and shadowing it would be an
	// easy read-vs-write mixup in a file that now does both.
	sessionops "github.com/jonhill90/keelson/internal/session"
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
		showBoard = flag.Bool("board", false, "start on the task board pane (agent-tui#6) instead of home -- the "+
			"persistent rail stays on screen either way (agent-tui#38); [f2] reaches the board pane from any "+
			"start. Read-only: derives its columns fresh on every fetch, never stores one.")
		ledger = flag.String("ledger", envOr("AGENT_TUI_LEDGER", ""),
			"path to a ledger.sqlite3 to read for the board and the rail's per-lane task/age/needs-human "+
				"content (agent-tui#26) -- must point at a COPY, never the live supervisor's own ledger "+
				"(agent-tui#6's rule). Left unset, keelson now auto-discovers the live ledger at "+
				"~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3 and copies it to a temp path itself, "+
				"re-copying on every board fetch (including [r]) -- see resolveLedgerSource/ledgerCopier "+
				"(agent-tui#49 item 2). Set this explicitly only to point at a different ledger or a "+
				"hand-made copy; the ledger read itself (`sqlite3 PRAGMA query_only=1`, not `-readonly` -- see "+
				"internal/board/ledger.go) never opens whatever this resolves to more than once per fetch.")
		ghBin        = flag.String("gh-bin", envOr("AGENT_GH_BIN", "gh"), "gh binary for the board's issue/PR reads")
		sqliteBin    = flag.String("sqlite-bin", envOr("AGENT_SQLITE_BIN", "sqlite3"), "sqlite3 binary for the board's and rail's ledger reads")
		repositories = flag.String("repositories", os.Getenv("SUPERVISOR_REPOSITORIES"),
			"colon-separated name=path=owner/repo entries, same shape as agent-supervisor's SUPERVISOR_REPOSITORIES "+
				"(.env.example) -- the board unions this with every repo it discovers in the ledger's own source_urls, "+
				"so it never has to hardcode a list. Defaults to $SUPERVISOR_REPOSITORIES.")
		showCost = flag.Bool("cost", false, "start on the cost pane (agent-tui#4) instead of home -- [f3] reaches "+
			"it from any start (agent-tui#38). Reads ccusage, never reimplements its per-harness usage parse.")
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
		quotaBin = flag.String("quota-bin", os.Getenv("AGENT_TUI_QUOTA_BIN"),
			"path to agent-supervisor's scripts/supervisor/quota.sh, the one seam allowed to call `codexbar` "+
				"(agent-tui#49 item 3 -- real session/weekly usage percentages and reset info for the cost panel). "+
				"Defaults to <supervisor-repo>/scripts/supervisor/quota.sh once -supervisor-repo is resolved "+
				"(explicitly, by discovery, or by fallback -- see discoverSupervisorRepo); set this to override, "+
				"or to a path that does not exist to exercise the \"quota.sh may be untracked\" UNKNOWN path "+
				"(as#227). Never fabricates a percentage: quota.sh missing or any exit code other than 0 renders "+
				"\"unknown\", the same discipline -claude-block-limit's honesty constraint already holds ccusage to.")
		showGallery = flag.Bool("gallery", false, "start on the glyph gallery pane (agent-tui#11) instead of home -- "+
			"every lane state against every candidate glyph, including glyphs not yet in any set, each flagged "+
			"with whether it needs a Nerd Font. [f4] reaches it from any start (agent-tui#38).")
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

	// agent-tui#38: -board/-cost/-gallery are no longer separate programs --
	// they only choose which pane the shell OPENS on (shell.Model.WithStart).
	// agent-tui#49 item 2 dropped -board's old hard "no -ledger, refuse to
	// start" refusal: resolveLedgerSource now auto-discovers and copies the
	// live ledger (defaultLedgerLivePath/ledgerCopier, board.go), so the
	// only way -board still refuses to start is boardOK == false below --
	// discovery genuinely found nothing, not merely "you didn't pass -ledger".
	ledgerSrc, boardOK, boardUnavailable := resolveLedgerSource(*ledger, *sqliteBin)
	if *showBoard && !boardOK {
		fmt.Fprintln(os.Stderr, "keelson: -board unavailable --", boardUnavailable)
		os.Exit(1)
	}

	// supervisorRepoResolved implements agent-tui#49 item 1: a bare
	// `keelson` must open, never exit 1, just because -supervisor-repo was
	// not typed. -mcp-cmd bypasses this entirely (it names its own command
	// line, no repo path needed); an explicit -supervisor-repo is trusted
	// as given, unchanged. Only when neither was given does discovery run.
	supervisorRepoResolved := *supervisorRepo
	if supervisorRepoResolved == "" && *mcpCmd == "" {
		if cwd, err := os.Getwd(); err == nil {
			supervisorRepoResolved = discoverSupervisorRepo(cwd)
		}
	}

	// quota.sh lives inside the supervisor checkout -- resolved from
	// whichever -supervisor-repo this program ends up using (explicit,
	// discovered, or fallback), same as mcp_server.py already is. -quota-bin
	// overrides this outright; an -mcp-cmd-only launch (no repo path at
	// all) leaves this empty, which buildCostFetch treats as "no quota
	// source" -- unknown, never fabricated (agent-tui#49 item 3).
	resolvedQuotaBin := *quotaBin
	if resolvedQuotaBin == "" && supervisorRepoResolved != "" {
		resolvedQuotaBin = filepath.Join(supervisorRepoResolved, "scripts", "supervisor", "quota.sh")
	}
	var quotaRun cost.QuotaRunner
	if resolvedQuotaBin != "" {
		quotaRun = cost.ExecQuotaRunner(resolvedQuotaBin)
	}

	// Built once, used by the cost pane, the rail's default cost line, and
	// (via buildBoardFetch) nothing else -- one Fetcher, three consumers,
	// never a second cost implementation.
	costFetch := buildCostFetch(*ccusageBin, splitArgs(*ccusageArgs), *claudeBlockLimit, quotaRun, time.Now)

	// The shell's rail is ALWAYS on screen (agent-tui#38 acceptance item 2),
	// so every launch tries a supervisor connection, including a bare
	// -cost or -gallery start -- those panes themselves still read nothing
	// but ccusage/compiled-in glyph data, but the rail beside them wants
	// "sessions"/"lanes" regardless of which pane is active. Unlike before
	// agent-tui#49, a failed connect() no longer exits: client stays nil,
	// connectErr carries why, and the rail renders that as a visible
	// notice (its own fetchErr/sessionsErr rendering, unchanged) instead of
	// the whole program refusing to open -- exactly the "degrade, don't
	// fail closed" fix item 1 asks for.
	client, cleanup, connectErr := connect(supervisorRepoResolved, *python, *mcpCmd)
	if connectErr != nil {
		cleanup = func() {}
	}
	defer cleanup()

	lanesFetch := func() ([]lane.Lane, error) {
		if client == nil {
			return nil, fmt.Errorf("no supervisor connection: %w", connectErr)
		}
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
		if client == nil {
			return nil, fmt.Errorf("no supervisor connection: %w", connectErr)
		}
		text, err := client.CallTool("sessions", map[string]any{})
		if err != nil {
			return nil, err
		}
		return lane.DecodeSessions(text)
	}

	// NewMultiSession, not NewWithCost: agent-tui#13 is the regression that
	// shipped a rail showing one session's lanes when six sessions
	// (including `director`) exist. WithOps wires agent-tui#14's write path
	// (attach/detach/add/remove) in on top of #13's multi-session rail --
	// session.New(client) shares the exact same MCP connection every read
	// above already uses, never a second client and never tmux itself.
	// Skipped entirely when client == nil (agent-tui#49 item 1's degraded
	// launch): rail.Model's ops field is nil-safe by design (see its own
	// doc comment) and a nil client would panic the moment any write op ran.
	railModel := rail.NewMultiSession(sessionsFetch, lanesFetch, costFetch, *directorSession).
		WithTasks(buildTaskFetch(ledgerSrc, *sqliteBin)).
		WithTheme(activeTheme, themeNotice)
	if client != nil {
		railModel = railModel.WithOps(sessionops.New(client))
	}

	// boardOK/boardUnavailable were already resolved above (resolveLedgerSource)
	// so -board's own refusal (showBoard && !boardOK) and the shell's
	// navigation-time guard share exactly one source of truth -- see
	// shell.Model.unavailableView for how boardUnavailable renders when a
	// human reaches [f2] with boardOK false.
	boardFetch := buildBoardFetch(ledgerSrc, *ghBin, *sqliteBin, *repositories, lanesFetch)
	boardModel := board.NewWithRefreshInterval(boardFetch, *boardRefresh).WithTheme(activeTheme, themeNotice)

	costModel := cost.New(costFetch).WithTheme(activeTheme, themeNotice)
	galleryModel := gallery.New().WithTheme(activeTheme, themeNotice)

	start := shell.PaneHome
	switch {
	case *showBoard:
		start = shell.PaneBoard
	case *showCost:
		start = shell.PaneCost
	case *showGallery:
		start = shell.PaneGallery
	}

	m := shell.New(railModel, boardModel, boardOK, boardUnavailable, costModel, galleryModel).
		WithStart(start).
		WithTheme(activeTheme)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "keelson:", err)
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
