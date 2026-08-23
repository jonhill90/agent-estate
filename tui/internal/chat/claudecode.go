package chat

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrNoProjectDir is ClaudeCodeSource's own "not configured" answer --
// distinct from a real read error (a project dir exists but a file could
// not be parsed, surfaced as-is) and distinct from "configured, found
// zero sessions" (a nil error with an empty slice -- real emptiness, not
// this). FallbackSource (below) treats ONLY this error as "there is no
// real source here, use the fixture"; every other outcome -- a real
// error, or a real empty result -- is returned to the caller unchanged,
// never silently swapped for fixture content (agent-b3.md's own rule:
// "never let real emptiness render as fixture data").
var ErrNoProjectDir = errors.New("chat: no claude code project directory found")

// maxThreads/maxMessagesPerThread bound what ClaudeCodeSource returns --
// this machine's own ~/.claude/projects holds over a thousand project
// directories going back weeks, and individual session files run past
// 40MB (a single tool result can itself be over a megabyte -- verified
// against this box's own real transcripts, not assumed). Nothing this
// pane renders needs the full archive: maxThreads keeps the list to the
// most-recently-active sessions across the whole estate (this package's
// own doc comment's ambition -- "every agent's live stream rendered side
// by side" -- means across every project directory, not just one repo's
// own cwd), and maxMessagesPerThread keeps one transcript to a size a
// terminal pane can actually show, the same truncation discipline
// internal/board's BodySnippet and internal/library's progressive
// disclosure already use elsewhere in this module.
const (
	maxThreads           = 12
	maxMessagesPerThread = 60
)

// ClaudeCodeSource reads Claude Code CLI's own local session transcripts --
// one JSONL file per session under <projectsDir>/<project>/<session-id>.jsonl,
// the exact file this very process's own conversation is recorded into
// while it runs. Chosen over the other two candidates agent-b3.md named:
//
//   - agent-supervisor's own ledger.sqlite3 has a `prompts` table, but it
//     stores only Jon's own prompt text (text_raw/text_clean) -- never an
//     agent's replies or tool calls. Half a conversation is not a thread.
//   - agent-supervisor's daemon/internal/ledger package stores
//     {id, lane, summary, status, worktree_path, timestamps} -- task/lane
//     STATE, the same shape internal/board already reads from the OTHER
//     ledger's tasks/lanes tables. Zero message content of any kind.
//
// Claude Code's own transcripts are the only one of the three with a full
// session/update-shaped conversation (user text, assistant text,
// thinking, tool_use paired with its tool_result) already recorded on
// this machine, for every harness session that has ever run here --
// confirmed by reading real files under this box's own
// ~/.claude/projects, not assumed from a schema doc.
//
// Read-only, per agent-b3.md's own scope: this type never writes to
// anything under projectsDir.
type ClaudeCodeSource struct {
	// projectsDir is the resolved directory to scan -- cmd/keelson
	// resolves $CLAUDE_PROJECTS_DIR / os.UserHomeDir()+"/.claude/projects"
	// the same way every other seam in this module takes an
	// already-resolved path (knowledge.NewFetcher(vaultDir), the ledger
	// path board.ExecRunner reads) rather than reading an env var itself.
	// Empty is a legitimate, deliberate "nothing to resolve" input, not a
	// caller bug -- Threads() reports it as ErrNoProjectDir, the same
	// "typed absence, not a bare zero" AGENTS.md asks every new seam in
	// this repo to follow.
	projectsDir string
}

// NewClaudeCodeSource builds a ClaudeCodeSource reading projectsDir.
func NewClaudeCodeSource(projectsDir string) ClaudeCodeSource {
	return ClaudeCodeSource{projectsDir: projectsDir}
}

// Threads scans every project subdirectory's most-recently-modified
// session file, sorts the whole estate's sessions by that file's mtime,
// and parses the newest maxThreads into real Threads. A project directory
// with no *.jsonl file is skipped, not an error -- Claude Code creates a
// project directory before it ever writes a session file in some
// versions, and an empty directory is not evidence of anything wrong.
func (s ClaudeCodeSource) Threads() ([]Thread, error) {
	if s.projectsDir == "" {
		return nil, ErrNoProjectDir
	}
	entries, err := os.ReadDir(s.projectsDir)
	if errors.Is(err, os.ErrNotExist) {
		// Never used on this machine, or this cwd's own project dir
		// simply hasn't been created yet -- the same "not configured"
		// answer an empty projectsDir gives, not a real read failure.
		return nil, ErrNoProjectDir
	}
	if err != nil {
		return nil, err
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	var candidates []candidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		latest, ok := latestSessionFile(filepath.Join(s.projectsDir, e.Name()))
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{path: latest.path, modTime: latest.modTime})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modTime.After(candidates[j].modTime) })
	if len(candidates) > maxThreads {
		candidates = candidates[:maxThreads]
	}

	threads := make([]Thread, 0, len(candidates))
	for _, c := range candidates {
		th, err := parseSessionFile(c.path)
		if err != nil {
			// One unreadable/corrupt session file must not blank out
			// every other real thread this fetch already found -- the
			// same "fails independently, not all-or-nothing" discipline
			// internal/dashboard.Stats' own doc comment states for its
			// five figures. Skipped, not fabricated.
			continue
		}
		threads = append(threads, th)
	}
	return threads, nil
}

type sessionFile struct {
	path    string
	modTime time.Time
}

// latestSessionFile returns dir's own most-recently-modified *.jsonl file
// -- one thread per project directory, its newest session, not every
// historical session that directory has ever recorded. A directory with
// no *.jsonl at all (a project Claude Code created but never wrote a
// transcript into) returns ok == false.
func latestSessionFile(dir string) (sessionFile, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return sessionFile{}, false
	}
	var best sessionFile
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !found || info.ModTime().After(best.modTime) {
			best = sessionFile{path: filepath.Join(dir, e.Name()), modTime: info.ModTime()}
			found = true
		}
	}
	return best, found
}

// rawEnvelope is one JSONL line's own outer shape -- every line this
// format writes has "type"; only "user"/"assistant" carry a "message",
// only "ai-title" carries a title. Every other type this format emits
// (mode, permission-mode, file-history-snapshot, attachment, last-prompt,
// agent-name, ...) is metadata this package has no Message/Thread field
// for and is skipped, not guessed at.
type rawEnvelope struct {
	Type      string          `json:"type"`
	Message   json.RawMessage `json:"message"`
	Timestamp string          `json:"timestamp"`
	AITitle   string          `json:"aiTitle"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // a plain string, or a []rawContentBlock
}

type rawContentBlock struct {
	Type string `json:"type"` // "text", "thinking", "tool_use", "tool_result"

	Text     string `json:"text"`
	Thinking string `json:"thinking"`

	ID   string `json:"id"`   // tool_use's own id
	Name string `json:"name"` // tool_use's own tool name

	ToolUseID string `json:"tool_use_id"` // tool_result: which tool_use this answers
	IsError   bool   `json:"is_error"`
}

// parseSessionFile decodes one JSONL session transcript into a Thread.
// Reads line by line via bufio.Reader (not bufio.Scanner -- this format's
// own tool_result lines run past a megabyte on this box's real files,
// well past Scanner's fixed token ceiling; Reader.ReadBytes has none).
// Every field this function keeps is small (Text/ToolName/ToolStatus);
// the large raw tool_result payloads are read and discarded, never
// stored, so a whole session's worth of Messages stays cheap even when
// the file itself is tens of megabytes.
func parseSessionFile(path string) (Thread, error) {
	f, err := os.Open(path)
	if err != nil {
		return Thread{}, err
	}
	defer f.Close()

	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	var (
		messages  []Message
		toolIndex = map[string]int{} // tool_use id -> index into messages
		title     string
		lastAt    time.Time
		seq       int
	)

	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, readErr := r.ReadBytes('\n')
		line = bytesTrimSpace(line)
		if len(line) > 0 {
			var env rawEnvelope
			if err := json.Unmarshal(line, &env); err == nil {
				at := parseTimestamp(env.Timestamp)
				if at.After(lastAt) {
					lastAt = at
				}
				switch env.Type {
				case "ai-title":
					if env.AITitle != "" {
						title = env.AITitle
					}
				case "user":
					messages, seq = appendUserMessage(messages, toolIndex, env, at, seq)
				case "assistant":
					messages, seq = appendAssistantMessage(messages, toolIndex, env, at, seq)
				}
			}
		}
		if readErr != nil {
			break // io.EOF (or a genuine read error -- either way, this session is done)
		}
	}

	if len(messages) > maxMessagesPerThread {
		messages = messages[len(messages)-maxMessagesPerThread:]
	}
	if title == "" {
		title = "session " + shortID(id)
	}
	return Thread{
		ID:           id,
		Title:        title,
		Messages:     messages,
		LastActivity: lastAt,
	}, nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// appendUserMessage handles one "user" envelope -- either plain text (a
// real user prompt) or an array of tool_result blocks answering a prior
// assistant tool_use (matched by id via toolIndex, never appended as a
// message of its own: this package renders a tool call and its result as
// ONE block, not two, the same shape ACP's own session/update gives a
// tool call).
func appendUserMessage(messages []Message, toolIndex map[string]int, env rawEnvelope, at time.Time, seq int) ([]Message, int) {
	var msg rawMessage
	if err := json.Unmarshal(env.Message, &msg); err != nil {
		return messages, seq
	}

	// content is either a plain JSON string (an ordinary user prompt) or
	// an array of blocks (tool_result answers, and occasionally an
	// explicit text block) -- try the string shape first since it is the
	// common case and cheaper to detect than a failed array decode.
	var asString string
	if err := json.Unmarshal(msg.Content, &asString); err == nil {
		if asString != "" {
			messages = append(messages, Message{ID: msgID(at, seq), Kind: KindUserText, At: at, Text: asString})
			return messages, seq + 1
		}
		return messages, seq
	}

	var blocks []rawContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return messages, seq
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			if idx, ok := toolIndex[b.ToolUseID]; ok {
				if b.IsError {
					messages[idx].ToolStatus = ToolFailed
				} else {
					messages[idx].ToolStatus = ToolDone
				}
			}
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				messages = append(messages, Message{ID: msgID(at, seq), Kind: KindUserText, At: at, Text: b.Text})
				seq++
			}
		}
	}
	return messages, seq
}

// appendAssistantMessage handles one "assistant" envelope's content
// blocks -- text, thinking, and tool_use. A tool_use's own ToolStatus
// starts ToolPending and is updated in place (via toolIndex) once a
// matching tool_result arrives in a later "user" envelope; a tool call
// whose result never arrives before this session file ends (the process
// was killed mid-tool-call, or this is the live tail of a running
// session) stays ToolPending -- an honest "still running/unknown", never
// guessed as ToolDone.
func appendAssistantMessage(messages []Message, toolIndex map[string]int, env rawEnvelope, at time.Time, seq int) ([]Message, int) {
	var msg rawMessage
	if err := json.Unmarshal(env.Message, &msg); err != nil {
		return messages, seq
	}
	var blocks []rawContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return messages, seq
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				messages = append(messages, Message{ID: msgID(at, seq), Kind: KindAgentText, At: at, Text: b.Text})
				seq++
			}
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				messages = append(messages, Message{ID: msgID(at, seq), Kind: KindThought, At: at, Text: b.Thinking})
				seq++
			}
		case "tool_use":
			messages = append(messages, Message{
				ID: msgID(at, seq), Kind: KindToolCall, At: at,
				ToolName: b.Name, ToolStatus: ToolPending,
			})
			if b.ID != "" {
				toolIndex[b.ID] = len(messages) - 1
			}
			seq++
		}
	}
	return messages, seq
}

func msgID(at time.Time, seq int) string {
	return at.Format(time.RFC3339Nano) + "-" + strconv.Itoa(seq)
}

func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && isSpaceByte(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	for len(b) > 0 && isSpaceByte(b[0]) {
		b = b[1:]
	}
	return b
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

var _ Source = ClaudeCodeSource{}
