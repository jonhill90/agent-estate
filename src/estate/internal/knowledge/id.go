package knowledge

import "time"

// idLayout is Notebook-MCP's own 14-char id format.
const idLayout = "20060102150405"

// idClock issues one 14-char YYYYMMDDHHmmss id per call, each one second
// later than the last -- his own stated reason ("prevents agent
// collision"): id resolution is seconds, so two items generated in the
// same wall-clock second get the same id unless something offsets them.
// A single idClock is shared across one whole Generate call so every
// item it produces gets a distinct id, in source order.
type idClock struct {
	next time.Time
}

// newIDClock starts issuing ids at base (UTC).
func newIDClock(base time.Time) *idClock {
	return &idClock{next: base.UTC()}
}

// NextID returns the current id and advances the clock by one second.
func (c *idClock) NextID() string {
	id := c.next.Format(idLayout)
	c.next = c.next.Add(time.Second)
	return id
}
