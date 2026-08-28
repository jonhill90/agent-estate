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

# agent-estate#761: this tape depends on fixtures it never built, so a
# re-run on a clean host captured an error frame instead of the wired
# view. Build exactly what the header above documents -- three fixture
# skill dirs plus docs/eval-status.json with two of the three recorded,
# one of them could_not_measure -- before the first launch.
Type "rm -rf /tmp/atui151-fakehome /tmp/atui151-skillsrepo" Enter
Sleep 300ms
Type "mkdir -p /tmp/atui151-fakehome/.claude/skills/adopt-or-build /tmp/atui151-fakehome/.claude/skills/devils-advocate /tmp/atui151-fakehome/.claude/skills/never-invoked /tmp/atui151-skillsrepo/docs" Enter
Sleep 300ms
Type "printf '%b' '---\nname: adopt-or-build\ndescription: Fixture skill for agent-tui#151 VHS capture.\n---\n' > /tmp/atui151-fakehome/.claude/skills/adopt-or-build/SKILL.md" Enter
Sleep 300ms
Type "printf '%b' '---\nname: devils-advocate\ndescription: Fixture skill for agent-tui#151 VHS capture.\n---\n' > /tmp/atui151-fakehome/.claude/skills/devils-advocate/SKILL.md" Enter
Sleep 300ms
Type "printf '%b' '---\nname: never-invoked\ndescription: Fixture skill for agent-tui#151 VHS capture, deliberately unrecorded.\n---\n' > /tmp/atui151-fakehome/.claude/skills/never-invoked/SKILL.md" Enter
Sleep 300ms
# \x22 stands in for a literal " -- see skill-invocations.tape's own
# comment on why (VHS's Type "..." parser rejects a backslash-escaped
# embedded double quote). adopt-or-build gets a real verdict,
# devils-advocate is could_not_measure, never-invoked is left unrecorded
# so it renders "unevaluated" -- the three-way split this store proves.
Type "printf '{\x22skills\x22: {\x22adopt-or-build\x22: {\x22verdict\x22: \x22keep\x22, \x22date\x22: \x222026-08-20\x22}, \x22devils-advocate\x22: {\x22verdict\x22: \x22could_not_measure\x22, \x22date\x22: \x222026-08-21\x22}}}' > /tmp/atui151-skillsrepo/docs/eval-status.json" Enter
Sleep 300ms
Type "clear" Enter
Sleep 300ms
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
