// Command estate is the supervisor.
//
// It dispatches agent turns as subprocesses and records them durably. Delivery
// is a process exit and a parsed result -- never an inference from what a
// terminal pane appears to show. A turn that cannot be observed is recorded
// "unknown", which is not terminal: unknown is not failed, and it keeps
// occupying a slot until something says otherwise.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/pressure"
)

func usage() {
	fmt.Fprint(os.Stderr, `estate -- the supervisor

  estate pressure                       report whether the host can take work
  estate dispatch <issue> <brief-file>  run one agent turn, gated and recorded
  estate tasks                          latest state of every task
  estate inflight                       tasks still occupying a slot

Every gate fails closed: a limit that cannot be measured refuses.
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	l, err := ledger.Open(os.Getenv("ESTATE_LEDGER"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "estate: ledger unavailable:", err)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "pressure":
		v := pressure.Check(l, pressure.Default())
		fmt.Printf("load %.2f/core  free %.0fMB  inflight %d\n",
			v.Reading.LoadPerCore, v.Reading.FreeMemMB, v.Reading.InFlight)
		if !v.OK {
			for _, r := range v.Reasons {
				fmt.Fprintln(os.Stderr, "refuse: "+r)
			}
			os.Exit(1)
		}
		fmt.Println("within limits")

	case "tasks", "inflight":
		var rs []ledger.Record
		if os.Args[1] == "tasks" {
			rs, err = l.Current()
		} else {
			rs, err = l.InFlight()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		for _, r := range rs {
			fmt.Printf("%-28s %-10s %s %s\n", r.ID, r.State, r.At.Format(time.RFC3339), r.Issue)
		}
		fmt.Fprintf(os.Stderr, "%d task(s)\n", len(rs))

	case "dispatch":
		if len(os.Args) < 4 {
			usage()
			os.Exit(2)
		}
		issue, briefPath := os.Args[2], os.Args[3]
		brief, err := os.ReadFile(briefPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot read brief:", err)
			os.Exit(2)
		}
		// The operator's parameters are law and outrank the brief. If they
		// cannot be read we refuse: an agent working without them is exactly
		// how a month went into a layer the corpus had already ruled out.
		params, err := corpus.Hard()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: refusing to dispatch --", err)
			os.Exit(1)
		}
		grounded := corpus.Grounding(issue+" "+string(brief), params) + string(brief)

		if v := pressure.Check(l, pressure.Default()); !v.OK {
			fmt.Fprintln(os.Stderr, "estate: refusing to dispatch --")
			for _, r := range v.Reasons {
				fmt.Fprintln(os.Stderr, "  "+r)
			}
			os.Exit(1)
		}
		id := fmt.Sprintf("%s-%d", strings.TrimPrefix(issue, "#"), time.Now().UTC().Unix())
		if err := l.Append(ledger.Record{ID: id, Issue: issue, Lane: id, State: ledger.Dispatched}); err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot record dispatch:", err)
			os.Exit(2)
		}
		fmt.Printf("dispatched %s (grounded in %d operator parameters)\n", id, len(params))

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "json",
			"--dangerously-skip-permissions")
		cmd.Stdin = strings.NewReader(grounded)
		out, runErr := cmd.Output()

		rec := ledger.Record{ID: id, Issue: issue, Lane: id}
		switch {
		case ctx.Err() != nil:
			// Timed out. We do not know whether the turn did its work, so we
			// must not say it failed and must not free the slot.
			rec.State, rec.Note = ledger.Unknown, "timed out after 45m; unknown is not failed"
		case runErr != nil:
			rec.State, rec.Note = ledger.Failed, runErr.Error()
		default:
			rec.State = ledger.Complete
			var parsed map[string]any
			if json.Unmarshal(out, &parsed) == nil {
				if s, ok := parsed["result"].(string); ok {
					rec.Result = s
				}
			} else {
				// Exited 0 but we could not parse what it said. That is not a
				// clean completion; record it honestly.
				rec.State, rec.Note = ledger.Unknown, "exit 0 but result was not parseable JSON"
			}
		}
		if err := l.Append(rec); err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot record outcome:", err)
			os.Exit(2)
		}
		fmt.Printf("%s %s\n", id, rec.State)
		if rec.State != ledger.Complete {
			os.Exit(1)
		}

	default:
		usage()
		os.Exit(2)
	}
}
