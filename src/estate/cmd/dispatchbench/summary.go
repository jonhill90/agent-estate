package main

// The write-up, generated rather than typed, so the decision record can carry
// figures nobody transcribed by hand.
//
// ABSENCE IS PRINTED AS ABSENCE. A turn whose harness reported no dollar
// figure prints "--", never 0.00: the reader must be able to tell "we looked
// and it is zero" from "we could not look", which is the discipline
// internal/harness's Spend and src/tui's cost.Figure both apply to the same
// problem.

import (
	"fmt"
	"strings"
)

func oneLine(r turnResult) string {
	if r.Error != "" {
		return "FAILED: " + r.Error
	}
	share := "--"
	if v, ok := r.CachedShare(); ok {
		share = fmt.Sprintf("%.1f%% cached", v*100)
	}
	cost := "cost --"
	if r.CostUSD != nil {
		cost = fmt.Sprintf("$%.4f", *r.CostUSD)
	}
	return fmt.Sprintf("%5.1fs  %s  %s  peak worker %.0fMB", float64(r.WallMS)/1000, cost, share, r.Memory.PeakWorkerRSSMB)
}

func summarise(res runResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# dispatchbench\n\n")
	fmt.Fprintf(&b, "- started: %s\n- finished: %s\n", res.StartedAt, res.FinishedAt)
	fmt.Fprintf(&b, "- workload: %s\n", res.Workload)
	model := res.Model
	if model == "" {
		model = "(the harness's own default)"
	}
	fmt.Fprintf(&b, "- model: %s\n", model)
	fmt.Fprintf(&b, "- floor: abort below %.0fMB free, at or above %.0f swapouts per 2s sample, or above %.0fMB in one worker tree\n",
		res.Limits.MinFreeMemMB, res.Limits.MaxSwapoutsPerSample, res.Limits.MaxWorkerRSSMB)
	if res.StoppedWhy != "" {
		fmt.Fprintf(&b, "- **STOPPED EARLY: %s**\n", res.StoppedWhy)
	}
	fmt.Fprintf(&b, "\n## Per turn\n\n")
	fmt.Fprintf(&b, "| arm | turn | wall s | cost USD | input | cache read | cache create | output | cached share | peak worker MB | peak host MB | min free MB |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range res.Results {
		cost := "--"
		if r.CostUSD != nil {
			cost = fmt.Sprintf("%.4f", *r.CostUSD)
		}
		share := "--"
		if v, ok := r.CachedShare(); ok {
			share = fmt.Sprintf("%.1f%%", v*100)
		}
		note := ""
		if r.Error != "" {
			note = " (" + r.Error + ")"
		}
		fmt.Fprintf(&b, "| %s | %d | %.1f | %s | %d | %d | %d | %d | %s | %.0f | %.0f | %.0f |%s\n",
			r.Arm, r.Turn, float64(r.WallMS)/1000, cost,
			r.InputTokens, r.CacheReadTokens, r.CacheCreationTokens, r.OutputTokens,
			share, r.Memory.PeakWorkerRSSMB, r.Memory.PeakHostRSSMB, r.Memory.MinFreeMemMB, note)
	}

	fmt.Fprintf(&b, "\n## Per arm\n\n")
	fmt.Fprintf(&b, "| arm | turns answered | total cost USD | mean wall s | mean cached share | peak worker MB | token source |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|\n")
	for _, arm := range []string{"stateless", "persistent"} {
		var n, withCost int
		var cost, wall, shareSum, peak float64
		var shareN int
		var src string
		for _, r := range res.Results {
			if r.Arm != arm || !r.Answered {
				continue
			}
			n++
			src = r.TokenSource
			wall += float64(r.WallMS) / 1000
			if r.CostUSD != nil {
				cost += *r.CostUSD
				withCost++
			}
			if v, ok := r.CachedShare(); ok {
				shareSum += v
				shareN++
			}
			if r.Memory.PeakWorkerRSSMB > peak {
				peak = r.Memory.PeakWorkerRSSMB
			}
		}
		if n == 0 {
			continue
		}
		costCell := "--"
		if withCost > 0 {
			costCell = fmt.Sprintf("%.4f (over %d of %d turns)", cost, withCost, n)
		}
		shareCell := "--"
		if shareN > 0 {
			shareCell = fmt.Sprintf("%.1f%%", shareSum/float64(shareN)*100)
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %.1f | %s | %.0f | %s |\n", arm, n, costCell, wall/float64(n), shareCell, peak, src)
	}

	fmt.Fprintf(&b, "\n## Host, over the whole run\n\n")
	fmt.Fprintf(&b, "- peak host-wide resident: %.0fMB across %d samples\n", res.HostOverall.PeakHostRSSMB, res.HostOverall.Samples)
	fmt.Fprintf(&b, "- minimum free memory seen: %.0fMB (floor was %.0fMB)\n", res.HostOverall.MinFreeMemMB, res.Limits.MinFreeMemMB)
	fmt.Fprintf(&b, "- maximum swapouts in any 2s sample: %.0f (limit was %.0f)\n", res.HostOverall.MaxSwapoutRate, res.Limits.MaxSwapoutsPerSample)
	if res.LaneCost != "" {
		fmt.Fprintf(&b, "\n## The persistent lane's own /cost report\n\n```\n%s\n```\n", res.LaneCost)
	} else {
		fmt.Fprintf(&b, "\n## The persistent lane's own /cost report\n\nNot captured. No dollar figure is reported for the persistent arm rather than one computed from a price table.\n")
	}
	return b.String()
}
