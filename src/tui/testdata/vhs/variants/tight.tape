# Framing-variant capture: "tight" height, for agent-tui#128's follow-on
# (estate-loop/b2-screenshot-framing-variants) -- NOT a replacement for
# testdata/vhs/readme-demo.tape. That tape stays the source of truth for
# README.md until a height is actually chosen from these variants.
#
# Same walk, same cmd/demo fixture (all fake data, footer-disclosed), only
# `Set Height` differs (430 vs the production tape's 800) to show what a
# tighter crop looks like with real captures instead of an argued number.
# 430 is the shortest height that renders every destination's content
# cleanly -- sidebar "estate" header, the pane's own title/subtitle line,
# and the "ALL DATA ON THIS SCREEN IS FAKE" footer all present -- checked
# per destination, not guessed (heights below ~400 either failed to render
# in this harness or clipped the top header row; see the PR body). Re-run
# to refresh these:
#
#   vhs testdata/vhs/variants/tight.tape
#
Output testdata/vhs/variants/out/tight.gif
Set FontSize 13
Set Width 1400
Set Height 430
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-demo ./cmd/demo && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-demo" Enter
Sleep 3s
Screenshot docs/screenshots/variants/tight/home.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/tight/dashboard.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/tight/agents.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/tight/chat.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/tight/tasks.png

Down
Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/tight/library.png

Down
Enter
Sleep 1s
Screenshot docs/screenshots/variants/tight/lanes.png

Type "q"
