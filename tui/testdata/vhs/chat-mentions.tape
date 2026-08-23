# Visual QA for agent-tui#114: Chat as a room -- the composer's @-mention
# gate, driven through the real shell binary (cmd/keelson), not asserted
# from a unit test alone.
#
# fakemcp (cmd/fakemcp) now answers "sessions" as well as "session_send" --
# see its own doc comment -- with a fixed, deterministic two-lane roster:
# "alice" (state busy -- running) and "bob" (state dead -- known, but not
# running). That gives buildParticipantsFetch (cmd/keelson/chat.go) a real
# participant list to join against, without a live agent-supervisor daemon.
#
# Mutation-checked in both directions, per agent-tui#114's own verification
# list:
#   - @alice (real, running)      -> ValidateMentions passes, the send
#     proceeds to fakemcp's session_send exactly as an unmentioned message
#     would (chat-mentions-03-sent.png).
#   - @bob (real, NOT running)    -> refused at compose time, named as
#     "not running" (chat-mentions-04-refused-not-running.png).
#   - @ghost (not a participant)  -> refused at compose time, named as
#     "not in this room" (chat-mentions-05-refused-unknown.png), and the
#     composer's draft is preserved so the operator can fix the mention
#     without retyping the whole message.
#
# Run:
#   go build -o /tmp/fakemcp ./cmd/fakemcp
#   go build -o /tmp/atui-chat-mentions ./cmd/keelson
#   vhs testdata/vhs/chat-mentions.tape
Output testdata/vhs/out/chat-mentions.gif
Set FontSize 13
Set Width 1600
Set Height 900
Set Shell "bash"

Type "/tmp/atui-chat-mentions -mcp-cmd /tmp/fakemcp"
Enter
Sleep 3s

# Home -> Dashboard -> Agents -> Chat (3 downs), Enter -- same route every
# other chat tape uses.
Down
Down
Down
Enter
Sleep 2s

# [tab] into the pane, select a real thread (not the synthetic "All"),
# give the participant roster (2s refresh, model.go's
# participantsRefreshInterval) a moment to land before composing.
Tab
Sleep 300ms
Type "2"
Sleep 2500ms

# Direction 1: a real, running participant -- @alice resolves, the send
# proceeds exactly as an unmentioned message would (fakemcp's own
# 1.5s reply delay, same as chat-send.tape).
Type "i"
Sleep 300ms
Type "@alice please pick this up"
Screenshot testdata/vhs/out/chat-mentions-01-composing-valid.png
Enter
Sleep 2500ms
Screenshot testdata/vhs/out/chat-mentions-02-sent.png

# Direction 2: a real, but NOT running, participant -- @bob (fakemcp's
# lane state "dead") must be refused at compose time, never silently
# sent nowhere. Composer already cleared+closed by the delivered send
# above, so "i" re-opens it clean.
Type "i"
Sleep 200ms
Type "@bob still there?"
Enter
Sleep 500ms
Screenshot testdata/vhs/out/chat-mentions-03-refused-not-running.png

# Direction 3: a name that is not a participant at all -- @ghost. Same
# composer, same draft, different reason -- proves the two refusal
# reasons (mention.go's ValidateMentions) are named distinctly, not
# folded into one generic "cannot send" string.
Escape
Type "i"
Sleep 200ms
Type "@ghost are you real"
Enter
Sleep 500ms
Screenshot testdata/vhs/out/chat-mentions-04-refused-unknown.png

Type "q"
Sleep 1s
