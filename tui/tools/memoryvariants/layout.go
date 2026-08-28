package main

import "math"

// layout is a hand-rolled Fruchterman-Reingold spring embedder -- checked
// against `bubbles` first (agent-tui#61's brief: "check whether bubbles
// already provides what you are about to build" before hand-rolling
// anything). bubbles is not in go.mod (confirmed: `grep bubbles go.mod`
// finds nothing) and ships no graph/force primitive even where it is
// vendored elsewhere -- viewport/table/list are scrolling widgets, not
// layout. Hill90's own graph (services/ui/.../KnowledgeGraph.tsx) uses
// d3-force, which is not a Go module either. So this is the smallest
// correct thing, not a library gap papered over: ~40 lines, deterministic
// (no time.Now/rand -- ids seed a stable pseudo-random start position),
// fixed iteration count, same algorithm family d3-force implements.
//
// Positions land in an arbitrary float plane; callers snap to a character
// grid (grid-drag-spike variant) or map to cells directly (orbit variant
// doesn't use this at all -- see its own doc comment for why).
func layout(g graphData, width, height float64, iterations int) map[string]struct{ x, y float64 } {
	pos := make(map[string]struct{ x, y float64 }, len(g.nodes))
	for i, nd := range g.nodes {
		// Deterministic seed position: spread around a circle by index, not
		// randomized -- a spike that produces a different frame on every
		// `go run` would be useless for the frozen screenshots this tool
		// exists to produce.
		a := (float64(i) / float64(len(g.nodes))) * 2 * math.Pi
		pos[nd.id] = struct{ x, y float64 }{
			x: width/2 + math.Cos(a)*width*0.25,
			y: height/2 + math.Sin(a)*height*0.25,
		}
	}

	area := width * height
	k := math.Sqrt(area / math.Max(float64(len(g.nodes)), 1))

	for iter := 0; iter < iterations; iter++ {
		disp := make(map[string]struct{ x, y float64 }, len(g.nodes))

		// Repulsion: every pair of nodes pushes apart, force ~ k^2/d.
		for _, a := range g.nodes {
			for _, b := range g.nodes {
				if a.id == b.id {
					continue
				}
				pa, pb := pos[a.id], pos[b.id]
				dx, dy := pa.x-pb.x, pa.y-pb.y
				d := math.Hypot(dx, dy)
				if d < 0.01 {
					d = 0.01
				}
				f := (k * k) / d
				da := disp[a.id]
				da.x += (dx / d) * f
				da.y += (dy / d) * f
				disp[a.id] = da
			}
		}

		// Attraction: linked nodes pull together, force ~ d^2/k.
		for _, e := range g.edges {
			pa, pb := pos[e.from], pos[e.to]
			dx, dy := pa.x-pb.x, pa.y-pb.y
			d := math.Hypot(dx, dy)
			if d < 0.01 {
				d = 0.01
			}
			f := (d * d) / k
			da, db := disp[e.from], disp[e.to]
			da.x -= (dx / d) * f
			da.y -= (dy / d) * f
			db.x += (dx / d) * f
			db.y += (dy / d) * f
			disp[e.from] = da
			disp[e.to] = db
		}

		// Cooling: step size shrinks linearly to zero over the run so the
		// layout settles instead of oscillating forever.
		temp := (1 - float64(iter)/float64(iterations)) * (width / 10)
		for _, nd := range g.nodes {
			d := disp[nd.id]
			dl := math.Hypot(d.x, d.y)
			if dl < 0.01 {
				dl = 0.01
			}
			p := pos[nd.id]
			p.x += (d.x / dl) * math.Min(dl, temp)
			p.y += (d.y / dl) * math.Min(dl, temp)
			p.x = math.Max(1, math.Min(width-1, p.x))
			p.y = math.Max(1, math.Min(height-1, p.y))
			pos[nd.id] = p
		}
	}
	return pos
}
