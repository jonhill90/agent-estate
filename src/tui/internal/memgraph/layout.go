package memgraph

import "math"

// point is a position in the arbitrary float plane layout() computes --
// callers snap it to a character grid (model.go's own withLayout).
type point struct{ x, y float64 }

// layout is tools/memoryvariants/layout.go's own hand-rolled
// Fruchterman-Reingold spring embedder, PORTED (not imported -- see this
// package's own doc comment) unchanged in algorithm: deterministic seed
// positions (no time.Now/rand -- ids seed a stable pseudo-random start
// position from their own index), fixed iteration count, the same
// repulsion/attraction/cooling shape d3-force implements. See the
// original file's doc comment (still present in tools/memoryvariants,
// left alone per this issue's brief) for the adopt-or-build reasoning
// that produced it -- bubbles ships no graph/force primitive, and
// d3-force is not a Go module.
func layout(g Graph, width, height float64, iterations int) map[string]point {
	pos := make(map[string]point, len(g.Nodes))
	for i, nd := range g.Nodes {
		a := (float64(i) / float64(len(g.Nodes))) * 2 * math.Pi
		pos[nd.ID] = point{
			x: width/2 + math.Cos(a)*width*0.25,
			y: height/2 + math.Sin(a)*height*0.25,
		}
	}

	area := width * height
	k := math.Sqrt(area / math.Max(float64(len(g.Nodes)), 1))

	for iter := 0; iter < iterations; iter++ {
		disp := make(map[string]point, len(g.Nodes))

		// Repulsion: every pair of nodes pushes apart, force ~ k^2/d.
		for _, a := range g.Nodes {
			for _, b := range g.Nodes {
				if a.ID == b.ID {
					continue
				}
				pa, pb := pos[a.ID], pos[b.ID]
				dx, dy := pa.x-pb.x, pa.y-pb.y
				d := math.Hypot(dx, dy)
				if d < 0.01 {
					d = 0.01
				}
				f := (k * k) / d
				da := disp[a.ID]
				da.x += (dx / d) * f
				da.y += (dy / d) * f
				disp[a.ID] = da
			}
		}

		// Attraction: linked nodes pull together, force ~ d^2/k.
		for _, e := range g.Edges {
			pa, pb := pos[e.From], pos[e.To]
			dx, dy := pa.x-pb.x, pa.y-pb.y
			d := math.Hypot(dx, dy)
			if d < 0.01 {
				d = 0.01
			}
			f := (d * d) / k
			da, db := disp[e.From], disp[e.To]
			da.x -= (dx / d) * f
			da.y -= (dy / d) * f
			db.x += (dx / d) * f
			db.y += (dy / d) * f
			disp[e.From] = da
			disp[e.To] = db
		}

		// Cooling: step size shrinks linearly to zero over the run so the
		// layout settles instead of oscillating forever.
		temp := (1 - float64(iter)/float64(iterations)) * (width / 10)
		for _, nd := range g.Nodes {
			d := disp[nd.ID]
			dl := math.Hypot(d.x, d.y)
			if dl < 0.01 {
				dl = 0.01
			}
			p := pos[nd.ID]
			p.x += (d.x / dl) * math.Min(dl, temp)
			p.y += (d.y / dl) * math.Min(dl, temp)
			p.x = math.Max(1, math.Min(width-1, p.x))
			p.y = math.Max(1, math.Min(height-1, p.y))
			pos[nd.ID] = p
		}
	}
	return pos
}
