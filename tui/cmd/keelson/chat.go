package main

import (
	"github.com/jonhill90/keelson/internal/chat"
	"github.com/jonhill90/keelson/internal/lane"
)

// buildParticipantsFetch composes chat.ParticipantsFetcher from the exact
// same "sessions" MCP call main.go's sessionsFetch already makes for the
// rail and the Agents pane (internal/agents.New(sessionsFetch)) -- never a
// second reader of tmux/the ledger (AGENTS.md's adapter discipline). Every
// lane across every session becomes one chat.Participant, addressable by
// its own pane name -- agent-tui#114's own direction: "the estate's live
// lanes are visible to this binary already (the Agents pane reads them),
// so use that source" for the room's participant model.
//
// Running mirrors internal/agents/row.go's modeFor exactly: false only for
// lane states "dead"/"stale" (agent-supervisor's own verdict that the
// harness process itself is gone), true otherwise -- the same evidence,
// read the same way, so an agent this binary already shows as gone in the
// Agents pane cannot simultaneously validate as a live @-mention target
// here.
//
// A session sessions.sh could not read (sess.Error != "") contributes no
// participants for its own lanes, same as lane.Session's own doc comment
// requires elsewhere in this module -- unreadable, not "no lanes."
func buildParticipantsFetch(sessionsFetch func() ([]lane.Session, error)) chat.ParticipantsFetcher {
	if sessionsFetch == nil {
		return nil
	}
	return func() ([]chat.Participant, error) {
		sessions, err := sessionsFetch()
		if err != nil {
			return nil, err
		}
		var out []chat.Participant
		for _, sess := range sessions {
			if sess.Error != "" {
				continue
			}
			for _, l := range sess.Lanes {
				out = append(out, chat.Participant{
					Name:    l.Name,
					Session: sess.Name,
					Running: l.State != "dead" && l.State != "stale",
				})
			}
		}
		return out, nil
	}
}
