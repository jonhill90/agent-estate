# agent-tui#174 verification: the Skills pane's INVOCATIONS column, wired
# to internal/skills.InvocationFetcher's private per-machine cache. Not
# part of the standing full-nav-walk suite -- a one-off capture proving
# all three render states, the same role skills-eval-store.tape plays for
# LAST EVAL/VERDICT (agent-tui#151).
#
# $HOME is redirected to a fixture home (~/.claude/skills with three
# fixture skill dirs: adopt-or-build, devils-advocate, never-invoked) and
# -skills-repo to a fixture skills-repo checkout, so this capture never
# touches Jon's real checkout, real skills, or real invocation cache.
# -skill-invocations-cache is pointed at three different fixture cache
# paths across three separate launches of the same binary, one per
# render state:
#   1. a path that does not exist -> InvocationsNoHistory ("no local history")
#   2. a path with malformed JSON -> InvocationsStoreUnreadable ("store unreadable")
#   3. a path with a real cache -> real counts, including never-invoked's genuine 0
Output testdata/vhs/out/skill-invocations.gif
Set FontSize 13
Set Width 1600
Set Height 900
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-invocations174 ./cmd/estate && clear" Enter
Sleep 3s

# agent-estate#761: this tape depends on fixtures it never built, so a
# re-run on a clean host captured "! skills unavailable ... no such file
# or directory" at rc=0 instead of any of the three render states this
# tape exists to prove. Build exactly what the header above documents --
# no more, no less -- before the first launch, so this tape is
# reproducible by anyone, not just someone who built these by hand once.
Type "rm -rf /tmp/atui174-fakehome /tmp/atui174-skillsrepo /tmp/atui174-cache-missing.json /tmp/atui174-cache-corrupt.json /tmp/atui174-cache-real.json" Enter
Sleep 300ms
Type "mkdir -p /tmp/atui174-fakehome/.claude/skills/adopt-or-build /tmp/atui174-fakehome/.claude/skills/devils-advocate /tmp/atui174-fakehome/.claude/skills/never-invoked /tmp/atui174-skillsrepo" Enter
Sleep 300ms
Type "printf '%b' '---\nname: adopt-or-build\ndescription: Fixture skill for agent-tui#174 VHS capture.\n---\n' > /tmp/atui174-fakehome/.claude/skills/adopt-or-build/SKILL.md" Enter
Sleep 300ms
Type "printf '%b' '---\nname: devils-advocate\ndescription: Fixture skill for agent-tui#174 VHS capture.\n---\n' > /tmp/atui174-fakehome/.claude/skills/devils-advocate/SKILL.md" Enter
Sleep 300ms
Type "printf '%b' '---\nname: never-invoked\ndescription: Fixture skill for agent-tui#174 VHS capture, deliberately never invoked.\n---\n' > /tmp/atui174-fakehome/.claude/skills/never-invoked/SKILL.md" Enter
Sleep 300ms
# State 2's fixture: a cache file present but not valid JSON in the
# skillInvocationCache shape (truncated on purpose). \x22 stands in for a
# literal " -- VHS's own Type "..." parser does not accept a backslash
# escape for an embedded double quote (confirmed directly against v0.11.0),
# so every quote in a fixture's JSON content is written this way instead.
Type "printf '{\x22built_at\x22: \x22bad' > /tmp/atui174-cache-corrupt.json" Enter
Sleep 300ms
# State 3's fixture: a real, readable cache -- never-invoked's count is a
# genuine 0, not an absent entry, so the render must say "0", never
# InvocationsNoHistory (agent-tui#164's own distinction).
Type "printf '{\x22built_at\x22: \x222026-08-28T00:00:00Z\x22, \x22counts\x22: {\x22adopt-or-build\x22: 5, \x22devils-advocate\x22: 23, \x22never-invoked\x22: 0}}' > /tmp/atui174-cache-real.json" Enter
Sleep 300ms
# State 1's fixture is deliberate absence: /tmp/atui174-cache-missing.json
# is never created, matching InvocationsNoHistory's own "path does not
# exist" case (skill.go's loadInvocationCache).
Type "clear" Enter
Sleep 300ms
Show

# --- State 1: cache never built (InvocationsNoHistory) ---
Type "HOME=/tmp/atui174-fakehome /tmp/atui-invocations174 -skills-repo=/tmp/atui174-skillsrepo -skill-invocations-cache=/tmp/atui174-cache-missing.json"
Sleep 500ms
Enter
Sleep 3s
Down Down Down Down Down Down Down Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/skill-invocations-01-no-history.png
Ctrl+C
Sleep 500ms

# --- State 2: cache present but corrupt (InvocationsStoreUnreadable) ---
Type "HOME=/tmp/atui174-fakehome /tmp/atui-invocations174 -skills-repo=/tmp/atui174-skillsrepo -skill-invocations-cache=/tmp/atui174-cache-corrupt.json"
Enter
Sleep 3s
Down Down Down Down Down Down Down Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/skill-invocations-02-store-unreadable.png
Ctrl+C
Sleep 500ms

# --- State 3: cache present and readable (real counts, including a genuine 0) ---
Type "HOME=/tmp/atui174-fakehome /tmp/atui-invocations174 -skills-repo=/tmp/atui174-skillsrepo -skill-invocations-cache=/tmp/atui174-cache-real.json"
Enter
Sleep 3s
Down Down Down Down Down Down Down Down
Right
Sleep 300ms
Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/skill-invocations-03-real-counts.png

Sleep 1s
