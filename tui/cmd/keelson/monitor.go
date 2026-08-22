package main

import (
	"github.com/jonhill90/keelson/internal/lane"
	"github.com/jonhill90/keelson/internal/monitor"
)

// buildMonitorFetch composes monitor.ExecHostRunner (this machine's own
// load/swap/process reads -- new plumbing, monitor's own package doc
// comment on why no existing seam covers this) with sessionsFetch, the
// SAME "sessions" MCP call railModel/agentsModel/dashboardModel already
// share (main.go) -- not a second connection. Agent state counting mirrors
// buildDashboardFetch's own aggregation (dashboard.go) exactly, repeated
// here rather than imported since monitor.AgentHealth and dashboard.Stats
// are deliberately separate types (monitor/view.go's own doc comment).
func buildMonitorFetch(hostRunner monitor.HostRunner, sessionsFetch func() ([]lane.Session, error)) monitor.Fetcher {
	return func() (monitor.Snapshot, error) {
		var snap monitor.Snapshot

		if hostRunner != nil {
			host, err := hostRunner()
			if err != nil {
				snap.HostErr = err
			} else {
				snap.Host = host
			}
		}

		if sessionsFetch != nil {
			if sessions, err := sessionsFetch(); err == nil {
				counts := make(map[string]int)
				total := 0
				for _, session := range sessions {
					for _, l := range session.Lanes {
						counts[l.State]++
						total++
					}
				}
				snap.Agents = monitor.AgentHealth{Known: true, ByState: counts, Total: total}
			}
			// err != nil: Agents.Known stays false -- the sessions fetch
			// itself already distinguishes "no supervisor connection" from
			// "connected, zero lanes" (main.go's own sessionsFetch), and
			// that distinction is preserved here by simply not guessing.
		}

		return snap, nil
	}
}
