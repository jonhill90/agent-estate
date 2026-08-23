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
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	wishssh "github.com/charmbracelet/ssh"

	"github.com/jonhill90/keelson/internal/admin"
	"github.com/jonhill90/keelson/internal/agents"
	"github.com/jonhill90/keelson/internal/apidocs"
	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/chat"
	"github.com/jonhill90/keelson/internal/connectors"
	"github.com/jonhill90/keelson/internal/cost"
	"github.com/jonhill90/keelson/internal/dashboard"
	"github.com/jonhill90/keelson/internal/external"
	"github.com/jonhill90/keelson/internal/flow"
	"github.com/jonhill90/keelson/internal/gallery"
	"github.com/jonhill90/keelson/internal/knowledge"
	"github.com/jonhill90/keelson/internal/lane"
	"github.com/jonhill90/keelson/internal/library"
	"github.com/jonhill90/keelson/internal/mcp"
	"github.com/jonhill90/keelson/internal/mcpservers"
	"github.com/jonhill90/keelson/internal/monitor"
	"github.com/jonhill90/keelson/internal/rail"
	"github.com/jonhill90/keelson/internal/shell"
	"github.com/jonhill90/keelson/internal/skills"
	"github.com/jonhill90/keelson/internal/sshserver"
	"github.com/jonhill90/keelson/internal/theme"
	"github.com/jonhill90/keelson/internal/workflows"
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
		claudeProjectsDir = flag.String("claude-projects-dir", os.Getenv("AGENT_TUI_CLAUDE_PROJECTS_DIR"),
			"directory holding Claude Code CLI's own per-project session transcripts (chat.ClaudeCodeSource's "+
				"real source -- see its own doc comment for why this and not agent-supervisor's ledger). "+
				"Left unset, defaults to $HOME/.claude/projects; left unresolvable (no $HOME either) or "+
				"pointed at a path that does not exist, the chat pane falls back to chat.FixtureSource and "+
				"says so on screen (chat.FallbackSource) rather than rendering fixture content silently.")
		ledger = flag.String("ledger", envOr("AGENT_TUI_LEDGER", ""),
			"path to a ledger.sqlite3 to read for the board and the rail's per-lane task/age/needs-human "+
				"content (agent-tui#26) -- must point at a COPY, never the live supervisor's own ledger "+
				"(agent-tui#6's rule). Left unset, keelson now auto-discovers the live ledger at "+
				"~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3 and copies it to a temp path itself, "+
				"re-copying on every board fetch (including [r]) -- see resolveLedgerSource/ledgerCopier "+
				"(agent-tui#49 item 2). Set this explicitly only to point at a different ledger or a "+
				"hand-made copy; the ledger read itself (`sqlite3 PRAGMA query_only=1`, not `-readonly` -- see "+
				"internal/board/ledger.go) never opens whatever this resolves to more than once per fetch.")
		openAPISpec = flag.String("openapi", envOr("AGENT_TUI_OPENAPI", ""),
			"OpenAPI document rendered by the Docs -> API Docs pane. Empty falls back to "+
				"$HILL90_APP_REPO/"+openAPIRelPath+"; with neither, that pane says so rather than "+
				"showing an empty table.")
		hill90AppRepo = flag.String("hill90-app-repo", os.Getenv("HILL90_APP_REPO"),
			"local hill90-app checkout, used only to find the OpenAPI document above")
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
		showFlow = flag.Bool("flow", false, "start on the flow pane (agent-tui#64) instead of home -- a live view of "+
			"work moving between dispatched/working/review/blocked/done, reading the exact same Snapshot -board "+
			"does (never a second gh/ledger read; needs the same -ledger the board does). [f5] reaches it from "+
			"any start (agent-tui#38).")
		showChat = flag.Bool("chat", false, "start on the chat pane (agent-tui#20) instead of home -- threads as "+
			"ACP sessions, live via session/update once a lane runs on a structured transport. [f6] reaches it "+
			"from any start (agent-tui#38) -- [f5] was already claimed by -flow (agent-tui#64) by the time this "+
			"landed. No lane in this estate is on 'acp' or 'pi-rpc' today (agent-tui#20's own finding), so this "+
			"renders internal/chat.FixtureSource -- visibly synthetic data proving the seam, never a live "+
			"transcript.")
		boardRefresh = flag.Duration("board-refresh", envOrDuration("AGENT_TUI_BOARD_REFRESH", board.DefaultRefreshInterval),
			"how often -board re-fetches on its own tick (agent-tui#28). The previous hardcoded 5s measured at "+
				"~8,160 GitHub GraphQL points/hr against gh issue list/gh pr list -- against a shared 5,000/hr "+
				"budget, enough to starve agent-supervisor's own dispatch (agent-supervisor#144). This is a "+
				"screen a human reads, not a control loop; [r] still refreshes on demand. Defaults to "+
				"$AGENT_TUI_BOARD_REFRESH.")
		sshAddr = flag.String("ssh-addr", envOr("AGENT_TUI_SSH_ADDR", ""),
			"agent-tui#67: if set (e.g. \":2222\"), serve the exact same shell over SSH at this address instead of "+
				"opening a local Bubble Tea program -- every connecting client gets its own program and its own "+
				"copy of the same shell.Model, reading the same fetchers/MCP connection this process already "+
				"built. Defaults to $AGENT_TUI_SSH_ADDR; leaving both unset keeps today's local-only behaviour "+
				"unchanged.")
		sshHostKeyPath = flag.String("ssh-host-key-path", envOr("AGENT_TUI_SSH_HOST_KEY_PATH", ""),
			"path to the SSH host key; generated (ed25519) on first run if missing. Defaults to "+
				"$AGENT_TUI_SSH_HOST_KEY_PATH, or ~/.keelson/ssh_host_ed25519_key if that is unset too. Only "+
				"read when -ssh-addr is set.")
		sshAuthorizedKeys = flag.String("ssh-authorized-keys", envOr("AGENT_TUI_SSH_AUTHORIZED_KEYS", ""),
			"path to an authorized_keys-format file (same format as ~/.ssh/authorized_keys) -- only clients "+
				"presenting a listed public key may connect. Required for -ssh-addr to start unless "+
				"-ssh-insecure is passed explicitly; agent-tui#67 makes auth a deliberate choice, never an "+
				"agent-picked default. Defaults to $AGENT_TUI_SSH_AUTHORIZED_KEYS.")
		sshInsecure = flag.Bool("ssh-insecure", os.Getenv("AGENT_TUI_SSH_INSECURE") == "1",
			"agent-tui#67: skip public-key auth entirely -- ANY client that can reach -ssh-addr gets a full "+
				"attached session, no key check at all. Only takes effect with -ssh-addr set; refuses silently "+
				"otherwise (there is nothing to be insecure about). Off by default; logs a loud warning to "+
				"stderr on every start when set. Defaults to $AGENT_TUI_SSH_INSECURE=1.")
	)
	flag.Parse()

	// agent-tui#27: the one place a user's theme preference is resolved at
	// startup. theme.Load's three outcomes (missing/malformed/unknown --
	// see its own doc comment) are honored exactly as it returns them: a
	// missing config renders as today (activeTheme falls back to
	// theme.Default with an empty notice), while a malformed config or an
	// unknown theme name still resolves to theme.Default but carries a
	// notice every pane renders visibly -- #27 acceptance item 3, "an
	// undeterminable preference is never silently treated as a valid one."
	// activeTheme/themeNotice are handed to shell.Model.WithTheme below,
	// once -- agent-tui#51 moved the RUNTIME half of this (every pane
	// getting the SAME theme, including after a live 't' cycle) out of
	// this file and into shell.Model, since main only ever runs Load once
	// at process start and has no way to keep four independent pane copies
	// in sync with a keypress it never sees.
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
	// -flow needs the exact same board.Snapshot -board does (see flowModel's
	// own doc comment below) -- same refusal-to-start rule, same reason.
	if *showFlow && !boardOK {
		fmt.Fprintln(os.Stderr, "keelson: -flow unavailable --", boardUnavailable)
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
	//
	// agent-tui#51: none of the four pane models below get their own
	// WithTheme(activeTheme, themeNotice) call any more -- shell.Model is
	// now the single owner of the theme value (see internal/shell's own
	// theme field doc comment and theme.CycleRequestedMsg), and its own
	// WithTheme below fans activeTheme/themeNotice out to all four via
	// applyTheme. Threading it into each pane here AND again in shell
	// would recreate #48's exact defect the moment the two calls drift.
	railModel := rail.NewMultiSession(sessionsFetch, lanesFetch, costFetch, *directorSession).
		WithTasks(buildTaskFetch(ledgerSrc, *sqliteBin)).
		// *session is the same flag lanesFetch (above) already reads to
		// build the "lanes" MCP call -- rail.Model's own single-session
		// render path (New/NewWithCost's flat view, and NewMultiSession's
		// at#18 sessions-fetch-failed fallback, both lanesFetch feeds) has
		// no other way to learn which tmux session its own lane.Lane
		// values came from, and needs that name to build the ledger's own
		// "<session>:<window-index>" task-join key (rail.Model.sessionName's
		// own doc comment). Empty -session (the flag's own documented
		// "supervisor's default" case) degrades WithSessionName("") to the
		// same honest "(no task)" a lane the ledger never tracked already
		// renders -- never a guessed session name.
		WithSessionName(*session)
	if client != nil {
		railModel = railModel.WithOps(sessionops.New(client))
	}

	// boardOK/boardUnavailable were already resolved above (resolveLedgerSource)
	// so -board's own refusal (showBoard && !boardOK) and the shell's
	// navigation-time guard share exactly one source of truth -- see
	// shell.Model.unavailableView for how boardUnavailable renders when a
	// human reaches [f2] with boardOK false.
	boardFetch := buildBoardFetch(ledgerSrc, *ghBin, *sqliteBin, *repositories, lanesFetch)
	boardModel := board.NewWithRefreshInterval(boardFetch, *boardRefresh)

	costModel := cost.New(costFetch)
	galleryModel := gallery.New()
	// flowModel reads no Fetcher of its own (internal/flow's own doc
	// comment) -- shell.Model pushes boardModel's own Snapshot into it
	// after every board.Update, so this is never a second gh/ledger read.
	flowModel := flow.New()
	// chat.FallbackSource, agent-b3.md's own fix: agent-supervisor's MCP
	// surface still has no tool that returns message content (a
	// screen-scraped transcript was rejected for the same reason
	// agent-tui#20 itself gives -- send-keys panes have no message
	// boundaries to recover), but Claude Code CLI's own local session
	// transcripts under resolveClaudeProjectsDir's own path ARE a real,
	// already-recorded session/update-shaped conversation -- see
	// chat.ClaudeCodeSource's own doc comment for why this beats the
	// other two candidates considered (agent-supervisor's ledger.sqlite3
	// `prompts` table, its daemon's own task-only ledger). Falls back to
	// chat.FixtureSource, visibly, when no project directory can be
	// resolved at all -- chat.FallbackSource's own doc comment.
	chatModel := chat.New(chat.NewFallbackSource(chat.NewClaudeCodeSource(resolveClaudeProjectsDir(*claudeProjectsDir))))
	// chat.WithSender wires SPEC-shell.md S7's sending half --
	// agent-supervisor#508/#509's session_send, reached the exact way
	// #103's own investigation said it had to be: through the supervisor
	// MCP surface, sessionops.New(client) (already built above for the
	// rail's own write path), never a second subprocess transport in this
	// repo. session.ErrSendUnknown is translated to chat.ErrUnknown right
	// here at the seam boundary -- the one place this program is allowed
	// to know about both packages -- so internal/chat itself never needs
	// to import internal/session (Sender's own doc comment). Skipped
	// entirely when client == nil, the same degraded-launch guard
	// WithOps above already follows: chatModel.sender stays nil, and
	// trySend's own honest "cannot send" message covers it (Sender's own
	// doc comment; no code change needed here for that case).
	if client != nil {
		sessionOps := sessionops.New(client)
		chatModel = chatModel.WithSender(func(threadID, text string) error {
			_, err := sessionOps.Send(threadID, text)
			if err != nil && errors.Is(err, sessionops.ErrSendUnknown) {
				return fmt.Errorf("%w: %v", chat.ErrUnknown, err)
			}
			return err
		})
	}

	// agentsModel/skillsModel/mcpserversModel/connectorsModel/adminModel
	// are S6/S8/S9/S10/S11's own panes -- this is the first time any of
	// the five is wired into the shell (previously each shipped
	// standalone, driven only by its own throwaway demo binary). Each
	// reuses a seam this file already built for another pane rather than
	// opening a second connection to the same source:
	//   - agentsModel reuses sessionsFetch (the same "sessions" MCP call
	//     the rail already makes), the same ledger task fetch the rail
	//     uses via WithTasks, and (new, S6/S7 depth) a ledger+ccusage join
	//     for per-lane Cost -- buildAgentCostFetch (agents.go), reusing the
	//     same sqliteBin/ccusageBin/ccusageArgs this file already resolved
	//     for buildTaskFetch/buildCostFetch, not a third binary.
	//   - skillsModel/mcpserversModel/adminModel each read a real local
	//     path (no MCP, no supervisor) -- homeDir resolves once, below,
	//     and an unresolvable one (os.UserHomeDir erroring) degrades each
	//     of these three to "not wired" rather than failing the whole
	//     program, the same "wiring is optional" convention every other
	//     optional pane input in this file already follows (boardFetch's
	//     own ledgerSrc, costFetch's own quotaRun).
	//   - connectorsModel reads the three harness config paths ccusage
	//     already knows about (claude/codex/pi), independent of homeDir
	//     resolving or not.
	homeDir, homeDirErr := os.UserHomeDir()

	agentsModel := agents.New(sessionsFetch).
		WithTasks(agents.TaskFetcher(buildTaskFetch(ledgerSrc, *sqliteBin))).
		WithCosts(buildAgentCostFetch(ledgerSrc, *sqliteBin, *ccusageBin, splitArgs(*ccusageArgs)))

	var skillsModel skills.Model
	var mcpserversModel mcpservers.Model
	var adminModel admin.Model
	if homeDirErr == nil {
		skillsModel = skills.New(skills.ScanFetcher(filepath.Join(homeDir, ".claude", "skills")))

		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			cwd = ""
		}
		mcpserversModel = mcpservers.New(mcpservers.NewFetcher(filepath.Join(homeDir, ".claude.json"), cwd, exec.LookPath))

		adminModel = admin.New(admin.NewFetcher(admin.DockerExecRunner("docker"), exec.LookPath, theme.ConfigPath()))
	} else {
		skillsModel = skills.New(nil)
		mcpserversModel = mcpservers.New(nil)
		adminModel = admin.New(nil)
	}

	var connectorsPaths connectors.Paths
	if homeDir != "" {
		connectorsPaths = connectors.Paths{
			ClaudeConfig:     filepath.Join(homeDir, ".claude.json"),
			CodexConfig:      filepath.Join(homeDir, ".codex", "config.toml"),
			CodexModelsCache: filepath.Join(homeDir, ".codex", "models_cache.json"),
			PiSettings:       filepath.Join(homeDir, ".pi", "agent", "settings.json"),
		}
	}
	connectorsModel := connectors.New(connectors.NewFetcher(connectorsPaths))

	// dashboardModel reuses sessionsFetch/costFetch (the same closures
	// railModel/agentsModel/costModel already call) and board.FetchPRs
	// over the same gh runner shape buildBoardFetch already builds -- see
	// buildDashboardFetch's own doc comment (dashboard.go) for why none of
	// this is a second reader. $AGENT_MEMORY_VAULT resolved once, the same
	// env var internal/knowledge's own package doc names as the source of
	// truth -- empty is a real, silent "no vault configured" (VaultFacts
	// stays unknown), not an error, matching every other optional input
	// in this file.
	dashboardModel := dashboard.New(buildDashboardFetch(*ghBin, *repositories, sessionsFetch, costFetch, os.Getenv("AGENT_MEMORY_VAULT")))

	start := shell.PaneHome
	switch {
	case *showBoard:
		start = shell.PaneBoard
	case *showCost:
		start = shell.PaneCost
	case *showGallery:
		start = shell.PaneGallery
	case *showFlow:
		start = shell.PaneFlow
	case *showChat:
		start = shell.PaneChat
	}

	libraryModel := library.New(
		buildLibraryFetch(ledgerSrc, *sqliteBin),
		buildLibraryDetailLoader(ledgerSrc, *sqliteBin),
		buildLibraryCountFetch(ledgerSrc, *sqliteBin),
	)

	// monitorModel/workflowsModel are w5f.md's two new panes -- see
	// monitor.go/workflows.go's own doc comments for what each reuses
	// (sessionsFetch and the ledger task-fetch seam, respectively) and what
	// is genuinely new (monitor.ExecHostRunner, this machine's own reads).
	monitorModel := monitor.New(buildMonitorFetch(monitor.ExecHostRunner(), sessionsFetch))
	workflowsModel := workflows.New(buildWorkflowsFetch(ledgerSrc, *sqliteBin))
	// knowledgeModel reads $AGENT_MEMORY_VAULT -- the same env var
	// dashboardModel's own VaultFacts figure already reads (see the
	// comment at that call site) -- via internal/knowledge's own
	// Fetcher/FactLoader seams. Empty is a real, silent "no vault
	// configured" the package's own LoadIndex already turns into a
	// visible notice, not an error this file needs to guard against.
	vaultDir := os.Getenv("AGENT_MEMORY_VAULT")
	knowledgeModel := knowledge.New(
		knowledge.NewFetcher(vaultDir),
		knowledge.NewFactLoader(vaultDir),
	)

	// apidocsModel is Docs -> API Docs: hill90-app's own OpenAPI document
	// (internal/apidocs' package doc comment traces why that file is the
	// real source and not a list kept here). externalModel is Docs ->
	// Platform Docs and any future nav.KindExternal entry -- it holds no
	// data, only the destination the nav tree carries and the one seam
	// that can open it.
	apidocsModel := apidocs.New(buildAPIDocsFetch(resolveOpenAPISpec(*openAPISpec, *hill90AppRepo)))
	externalModel := external.New(browserOpener())

	m := shell.New(railModel, boardModel, boardOK, boardUnavailable, costModel, galleryModel, flowModel, chatModel).
		WithAgents(agentsModel).
		WithSkills(skillsModel).
		WithMCPServers(mcpserversModel).
		WithConnectors(connectorsModel).
		WithAdmin(adminModel).
		WithDashboard(dashboardModel).
		WithLibrary(libraryModel).
		WithMonitor(monitorModel).
		WithWorkflows(workflowsModel).
		WithKnowledge(knowledgeModel).
		WithAPIDocs(apidocsModel).
		WithExternal(externalModel).
		WithStart(start).
		WithTheme(activeTheme, themeNotice).
		WithThemeSave(func(th theme.Theme) error { return theme.Save(theme.ConfigPath(), th.ID) })

	// agent-tui#67: -ssh-addr set means SERVE, not open locally -- one
	// process is either a local Bubble Tea program or an SSH listener
	// handing the same shell.Model out per connection, never both, so
	// there is only ever one place reading os.Stdin's raw mode at a time.
	if *sshAddr != "" {
		if err := runSSH(m, *sshAddr, *sshHostKeyPath, *sshAuthorizedKeys, *sshInsecure); err != nil {
			fmt.Fprintln(os.Stderr, "keelson:", err)
			os.Exit(1)
		}
		return
	}

	// WithMouseCellMotion, not WithMouseAllMotion: cell motion reports press,
	// release and drag, which is everything a click needs. All-motion adds an
	// event per pixel of pointer movement, which floods Update for no gain
	// here -- and a flooded Update is how a TUI starts feeling slow.
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "keelson:", err)
		os.Exit(1)
	}
}

// runSSH serves seed (a fully-built shell.Model, identical to the one the
// local launch path would run) to every connecting SSH client until an
// interrupt/TERM signal arrives, then shuts the listener down gracefully.
// hostKeyPath "" resolves to ~/.keelson/ssh_host_ed25519_key so a bare
// -ssh-addr with no -ssh-host-key-path still has somewhere durable to write
// the generated key (an empty path would otherwise mean "cwd", surprising
// for a background-run server).
func runSSH(seed shell.Model, addr, hostKeyPath, authorizedKeys string, insecure bool) error {
	if hostKeyPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve default -ssh-host-key-path: %w", err)
		}
		hostKeyPath = filepath.Join(home, ".keelson", "ssh_host_ed25519_key")
	}
	if err := os.MkdirAll(filepath.Dir(hostKeyPath), 0o700); err != nil {
		return fmt.Errorf("create host key directory: %w", err)
	}

	if insecure {
		fmt.Fprintln(os.Stderr, "keelson: WARNING -ssh-insecure is set -- any client that can reach", addr,
			"gets a full attached session with NO key check at all")
	} else if authorizedKeys == "" {
		return fmt.Errorf("-ssh-addr requires -ssh-authorized-keys (or the explicit opt-out -ssh-insecure)")
	}

	fmt.Fprintln(os.Stderr, "keelson: serving SSH at", addr, "(host key:", hostKeyPath+")")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := sshserver.Config{
		Addr:           addr,
		HostKeyPath:    hostKeyPath,
		AuthorizedKeys: authorizedKeys,
		Insecure:       insecure,
		New: func(sess wishssh.Session) (tea.Model, []tea.ProgramOption) {
			opts := append(sshserver.MakeOptions(sess), tea.WithAltScreen())
			return seed, opts
		},
	}
	return sshserver.ListenAndServe(ctx, cfg)
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

// resolveClaudeProjectsDir returns explicit unchanged when set (a human
// or $AGENT_TUI_CLAUDE_PROJECTS_DIR chose it deliberately); otherwise
// $HOME/.claude/projects, the same directory Claude Code CLI itself
// writes every session transcript into. Empty return (no $HOME either) is
// a legitimate, typed "could not resolve anything" -- chat.ClaudeCodeSource
// reports it as chat.ErrNoProjectDir and chat.FallbackSource falls back to
// the fixture, visibly, rather than this function guessing a path that
// may not exist.
func resolveClaudeProjectsDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
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
