# Evidence for w5f.md's third decision: Connect -> Models is NOT given its
# own real pane. internal/connectors.View already renders a "-- models --"
# section (Codex's own model catalog, the one real local model list this
# estate can read) inside the SAME pane Connect -> Connections opens --
# confirmed by reading internal/connectors/view.go directly before removing
# anything. A nav entry that will never carry content of its own is worse
# than no entry (w5f.md's own instruction), so "models" was removed from
# internal/nav's tree (nav.Build's own doc comment) rather than left as a
# tenth permanent stub. Run: vhs testdata/vhs/models-removed.tape
Output testdata/vhs/out/models-removed.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-modelsgone ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-modelsgone" Enter
Sleep 3s

# Home(0) -> Lanes(7): 7 downs. -> Build header: 1 down. -> Connect header:
# 1 down (Build collapsed, its 3 children skipped) -- 9 downs total.
Down
Down
Down
Down
Down
Down
Down
Down
Down
Sleep 300ms

# Right expands Connect -- the sidebar screenshot below shows its full,
# now-four-item child list (Connections, Storage, Discord, Secrets) with
# no "Models" row anywhere in it.
Right
Sleep 300ms
Screenshot testdata/vhs/out/01-connect-group-no-models-row.png

# Down once more selects Connections (Connect's first child) -- Enter opens
# it, showing the SAME model catalog a dedicated Models route would have
# shown, already on screen under its own "-- models --" section.
Down
Enter
Sleep 3s
Screenshot testdata/vhs/out/02-connections-pane-has-its-own-models-section.png
