// Package quota reports how much of the operator's token budget is left.
//
// His hard rule: at roughly 10% remaining, stop orchestrating work to lanes.
// The previous implementation could not enforce it. Its launchd job invoked
// `agent-supervisor/scripts/supervisor/quota-watch.sh` -- a repo that does not
// exist -- so its state file accumulated 3,727 consecutive UNKNOWN readings
// while `confirmed: SAFE` sat beside them and `blind_alarm_sent: 0` never
// moved. The week was exhausted with the meter dark.
//
// Two rules follow from that, and they are the whole design:
//
//  1. UNKNOWN IS NOT SAFE. A reading that could not be taken is a refusal,
//     never a pass. Blindness is the condition that cost him the week.
//  2. A STALE READING IS UNKNOWN. A number from hours ago describes a budget
//     that has since been spent. Freshness is part of validity, not a detail.
package quota

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// Reading is a point-in-time budget observation. Weekly is the window his
// stop-threshold refers to; Session is the shorter rolling window.
type Reading struct {
	WeeklyUsedPercent  float64
	SessionUsedPercent float64
	UpdatedAt          time.Time
	Age                time.Duration
}

func (r Reading) WeeklyRemaining() float64 { return 100 - r.WeeklyUsedPercent }

type payload struct {
	Usage struct {
		UpdatedAt string `json:"updatedAt"`
		Primary   struct {
			UsedPercent float64 `json:"usedPercent"`
		} `json:"primary"`
		Secondary *struct {
			UsedPercent float64 `json:"usedPercent"`
		} `json:"secondary"`
	} `json:"usage"`
}

// MaxAge past which a reading is treated as no reading at all.
const MaxAge = 20 * time.Minute

// Read takes a fresh reading. Every failure path returns an error: there is no
// value this function can return that means "could not tell, carry on".
func Read(now time.Time) (Reading, error) {
	cmd := exec.Command("codexbar", "usage", "--provider", "claude", "--json")
	out, err := cmd.Output()
	if err != nil {
		return Reading{}, fmt.Errorf("codexbar unreachable: %w", err)
	}
	var ps []payload
	if err := json.Unmarshal(out, &ps); err != nil {
		return Reading{}, fmt.Errorf("codexbar output unparseable: %w", err)
	}
	if len(ps) == 0 {
		return Reading{}, fmt.Errorf("codexbar returned no providers -- refusing to read that as budget available")
	}
	p := ps[0]
	if p.Usage.Secondary == nil {
		return Reading{}, fmt.Errorf("codexbar reported no weekly window -- the window the stop-threshold refers to is missing")
	}
	ts, err := time.Parse(time.RFC3339, p.Usage.UpdatedAt)
	if err != nil {
		return Reading{}, fmt.Errorf("codexbar timestamp %q unparseable: %w", p.Usage.UpdatedAt, err)
	}
	r := Reading{
		WeeklyUsedPercent:  p.Usage.Secondary.UsedPercent,
		SessionUsedPercent: p.Usage.Primary.UsedPercent,
		UpdatedAt:          ts,
		Age:                now.Sub(ts),
	}
	if r.Age > MaxAge {
		return Reading{}, fmt.Errorf("codexbar reading is %s old (limit %s) -- stale is unknown, and unknown is not safe", r.Age.Round(time.Second), MaxAge)
	}
	if r.Age < -2*time.Minute {
		return Reading{}, fmt.Errorf("codexbar reading is %s in the future -- clock skew, refusing to trust it", (-r.Age).Round(time.Second))
	}
	return r, nil
}

// StopThresholdPercent is his rule: at roughly 10% remaining, stop
// orchestrating work to the lanes.
const StopThresholdPercent = 10.0

// Allow reports whether more orchestrated work may start.
func Allow(r Reading) (bool, string) {
	if rem := r.WeeklyRemaining(); rem <= StopThresholdPercent {
		return false, fmt.Sprintf("weekly budget %.0f%% remaining, at or below the %.0f%% stop threshold", rem, StopThresholdPercent)
	}
	return true, ""
}
