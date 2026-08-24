# agent-tui#151 verification: Skills pane wired to jonhill90/skills'
# docs/eval-status.json (skills#230, e6c33a5) via skills.EvalStatusFetcher.
# Not part of the standing full-nav-walk suite -- a one-off capture proving
# the real view renders the merged verdicts, not a route this repo's own
# navwalk re-measures every pass.
#
# $HOME is redirected to a fixture home (~/.claude/skills with three
# fixture skill dirs) and -skills-repo to a fixture skills-repo checkout
# (docs/eval-status.json with two of the three skills recorded, one of
# them could_not_measure) so this capture never touches Jon's real
# checkout or its real 41-skill store.
Output testdata/vhs/out/skills-eval-store.gif
Set FontSize 13
Set Width 1600
Set Height 900
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-skills151 ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "HOME=/tmp/atui151-fakehome /tmp/atui-skills151 -skills-repo=/tmp/atui151-skillsrepo" Enter
Sleep 3s

# Home -> Build group (Skills, Workflows, MCP Servers): same path
# full-nav-walk.tape's own 08-build-skills capture uses (7 Downs to reach
# Lanes, one more onto the Build group header itself, then Right to expand
# it before the final Down reaches Skills, its first child).
Down Down Down Down Down Down Down Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/skills-eval-store-wired.png

Sleep 1s
