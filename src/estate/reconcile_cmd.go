package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/reconcile"
)

// runReconcile reports on every turn the ledger still calls in-flight, and
// with --apply records a terminal outcome for those that positively cannot be
// running.
//
// Reporting is the default. A command that reclaims slots by surprise is the
// wrong shape for a rule whose whole point is refusing to guess.
func runReconcile(l *ledger.Ledger, apply bool) {
	inflight, err := l.InFlight()
	if err != nil {
		fmt.Fprintln(os.Stderr, "estate:", err)
		os.Exit(2)
	}
	var cands []reconcile.Candidate
	for _, r := range inflight {
		wt := ""
		if i := strings.Index(r.Note, "worktree "); i >= 0 {
			wt = strings.TrimSpace(strings.SplitN(r.Note[i+len("worktree "):], ";", 2)[0])
		}
		cands = append(cands, reconcile.Candidate{
			ID: r.ID, Issue: r.Issue, Lane: r.Lane,
			State: string(r.State), At: r.At, Worktree: wt,
		})
	}
	vs := reconcile.Judge(cands, reconcile.Exists, time.Now())
	reclaimed := 0
	for _, v := range vs {
		mark := "keep  "
		if v.Reclaim {
			mark = "RECLAIM"
			reclaimed++
		}
		fmt.Printf("%s %-34s %s\n", mark, v.ID, v.Reason)
	}
	fmt.Printf("\n%d in flight, %d reclaimable\n", len(vs), reclaimed)
	if !apply {
		if reclaimed > 0 {
			fmt.Println("nothing was changed -- pass --apply to record these outcomes")
		}
		return
	}
	for i, v := range vs {
		if !v.Reclaim {
			continue
		}
		if err := l.Append(ledger.Record{
			ID: cands[i].ID, Issue: cands[i].Issue, Lane: cands[i].Lane,
			State: ledger.Failed,
			Note:  "reconciled: " + v.Reason,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
	}
	fmt.Printf("recorded %d outcome(s); the ledger is append-only, so the original records remain\n", reclaimed)
}
