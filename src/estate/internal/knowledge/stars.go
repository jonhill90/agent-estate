package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// starredRepo is the subset of `gh api user/starred`'s own JSON this
// package reads -- Topics is GitHub's own tagging, the only source of
// SynapticTags any reader here carries.
type starredRepo struct {
	FullName    string   `json:"full_name"`
	HTMLURL     string   `json:"html_url"`
	Description string   `json:"description"`
	Topics      []string `json:"topics"`
}

// defaultGHRunner shells out to the real gh binary -- the one live
// implementation; every test in this package supplies a fake instead
// (Config.RunGH).
func defaultGHRunner(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if errBuf.Len() > 0 {
			return nil, fmt.Errorf("%w: %s", err, bytes.TrimSpace(errBuf.Bytes()))
		}
		return nil, err
	}
	return out.Bytes(), nil
}

// starsSource reads every GitHub star via `gh api user/starred
// --paginate`, one JSON object per line (the -q filter below), and turns
// each into one Item. A source that cannot be read at all (gh missing,
// not authenticated, network down) returns a SourceResult with OK=false
// and the real error as Reason -- never a silently empty Items slice.
func starsSource(run func(args ...string) ([]byte, error), clock *idClock) (SourceResult, []Item) {
	if run == nil {
		run = defaultGHRunner
	}
	res := SourceResult{Name: "github-stars"}

	out, err := run("api", "user/starred", "--paginate",
		"-q", ".[] | {full_name, html_url, description, topics: (.topics // [])}")
	if err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	var items []Item
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var r starredRepo
		if err := dec.Decode(&r); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			res.OK = false
			res.Reason = fmt.Sprintf("malformed output from gh api user/starred: %v", err)
			return res, nil
		}
		if r.FullName == "" {
			continue
		}
		tier1 := r.FullName
		if r.Description != "" {
			tier1 = r.FullName + " -- " + r.Description
		}
		items = append(items, Item{
			ID:             clock.NextID(),
			Source:         "github-stars",
			Permalink:      r.HTMLURL,
			StructuralTags: []string{"github-stars"},
			SynapticTags:   hashtag(r.Topics),
			Tier1:          truncate(tier1, 200),
			Tier2:          r.Description,
			Tier3:          r.HTMLURL,
		})
	}

	res.OK = true
	res.Count = len(items)
	return res, items
}

// hashtag turns GitHub's own bare topic strings into #hashtag synaptic
// tags -- the operator's own convention keeps synaptic tags visibly
// distinct from structural ones (bare) even though the source data for
// both starts out as a plain string slice.
func hashtag(topics []string) []string {
	if len(topics) == 0 {
		return nil
	}
	out := make([]string, len(topics))
	for i, t := range topics {
		out[i] = "#" + t
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
