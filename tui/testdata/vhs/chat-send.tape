# Visual QA for agent-b3.md's fix: SPEC-shell.md S7's sending half --
# internal/chat.Sender wired against agent-supervisor#508/#509's
# session_send, driven through the real shell binary (cmd/keelson), not a
# unit test claiming it works.
#
# The MCP side is cmd/fakemcp (this repo's own new fixture, see its own
# doc comment) rather than a real agent-supervisor daemon: a real
# session_send genuinely drives a live `claude -p --resume` turn, and
# firing that blind against a real session purely to capture a screenshot
# is not something this run should do. fakemcp speaks the real MCP wire
# protocol and drives every other layer for real -- internal/mcp's own
# JSON-RPC framing, internal/session.Ops.Send's classification
# (delivered/failed/unknown), internal/chat.Model's async trySend/Update
# and its three-state composer render -- only the agent turn itself is
# faked, deterministically, by message content (FAKE_FAIL/FAKE_UNKNOWN).
#
# Run:
#   go build -o /tmp/fakemcp ./cmd/fakemcp
#   go build -o /tmp/atui-chat-send ./cmd/keelson
#   vhs testdata/vhs/chat-send.tape
Output testdata/vhs/out/chat-send.gif
Set FontSize 13
Set Width 1600
Set Height 900
Set Shell "bash"

Type "/tmp/atui-chat-send -mcp-cmd /tmp/fakemcp" Enter
Sleep 3s

# Home -> Dashboard -> Agents -> Chat (3 downs), Enter -- same route every
# other chat tape uses.
Down
Down
Down
Enter
Sleep 2s

# [tab] into the pane, select a real thread (not the synthetic "All"),
# then [i] to compose.
Tab
Sleep 300ms
Type "2"
Sleep 300ms
Type "i"
Sleep 300ms
Screenshot testdata/vhs/out/chat-send-01-composing.png

# Delivered: plain text, no FAKE_ marker -- fakemcp answers "delivered"
# after fakeReplyDelay (1.5s). Screenshot the in-flight state first (the
# whole reason fakemcp sleeps before replying), then the resolved one.
Type "hello agent, please continue"
Enter
Sleep 400ms
Screenshot testdata/vhs/out/chat-send-02-in-flight.png
Sleep 2s
Screenshot testdata/vhs/out/chat-send-03-delivered.png

# Failed: FAKE_FAIL marker -- fakemcp answers isError with the exact text
# shape SessionSendSource.write() raises for a confirmed failure. The
# composer is empty here (delivered, above, cleared it and left compose
# mode) so "i" re-enters cleanly.
Type "i"
Sleep 200ms
Type "FAKE_FAIL please"
Enter
Sleep 2s
Screenshot testdata/vhs/out/chat-send-04-failed.png

# A failed send deliberately STAYS in compose mode (the draft is not
# lost) -- "esc" then "i" gets a clean, empty composer for the next
# state rather than typing into whatever a failed send left behind.
Escape
Type "i"
Sleep 200ms

# Unknown: FAKE_UNKNOWN marker -- fakemcp answers isError carrying the
# literal "outcome UNKNOWN" marker Ops.Send's own unknownMarker looks
# for. Must render distinctly from failed (chat-send-04 above), never as
# "delivered" and never with failed's own "! " styling.
Type "FAKE_UNKNOWN please"
Enter
Sleep 2s
Screenshot testdata/vhs/out/chat-send-05-unknown.png

Type "q"
Sleep 1s
