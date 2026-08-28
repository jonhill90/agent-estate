# Framing-variant capture: "medium" height, for agent-tui#128's follow-on
# (estate-loop/b2-screenshot-framing-variants) -- NOT a replacement for
# testdata/vhs/readme-demo.tape. That tape stays the source of truth for
# README.md until a height is actually chosen from these variants.
#
# Same walk, same cmd/demo fixture (all fake data, footer-disclosed), only
# `Set Height` differs (580 vs the production tape's 800) to show a middle
# point between the tight variant and today's 800 -- still noticeably less
# dead space than production, with more breathing room than tight.tape.
# Re-run to refresh these:
#
#   vhs testdata/vhs/variants/medium.tape
#
Output testdata/vhs/variants/out/medium.gif
Set FontSize 13
Set Width 1400
Set Height 580
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-demo ./cmd/demo && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-demo" Enter
Sleep 3s
Screenshot docs/screenshots/variants/medium/home.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/medium/dashboard.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/medium/agents.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/medium/chat.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/medium/tasks.png

Down
Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/medium/library.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/medium/lanes.png

Type "q"
