package main

import (
	"github.com/jonhill90/agent-estate/src/tui/internal/chat"
	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
	"github.com/jonhill90/agent-estate/src/tui/internal/lane"
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
//
// agent-estate#930: sessionsFetch reads the deleted Python MCP server and
// is permanently unable to answer in this tree -- ledgerStatusFetch (the
// same estateLedger path main.go already resolves for agentsModel/
// dashboardModel/WithEstateStatus) fills in with the Go ledger's own
// in-flight dispatches, one Participant per turn, ID as Name (a ledger
// dispatch has no tmux pane name to offer) and Session left empty (see
// Participant.Session's own doc comment for why that is never mistaken for
// a real tmux session). This ADDS to whatever sessionsFetch returns rather
// than replacing it -- if tmux/MCP is ever restored, its participants keep
// appearing exactly as before, alongside the ledger's.
func buildParticipantsFetch(sessionsFetch func() ([]lane.Session, error), ledgerStatusFetch func() estatus.Status) chat.ParticipantsFetcher {
	if sessionsFetch == nil && ledgerStatusFetch == nil {
		return nil
	}
	return func() ([]chat.Participant, error) {
		var out []chat.Participant
		var sessionsErr error

		if sessionsFetch != nil {
			sessions, err := sessionsFetch()
			sessionsErr = err
			if err == nil {
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
			}
		}

		if ledgerStatusFetch != nil {
			status := ledgerStatusFetch()
			for _, d := range status.InFlight {
				out = append(out, chat.Participant{Name: d.ID, Running: true})
			}
		}

		// Only report the sessions-side error when it contributed nothing
		// at all -- if the ledger fallback found real participants, a dead
		// MCP server is not this fetch's own failure to report; ValidateMentions
		// still has a real roster to check @-mentions against.
		if sessionsErr != nil && len(out) == 0 {
			return nil, sessionsErr
		}
		return out, nil
	}
}
