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
