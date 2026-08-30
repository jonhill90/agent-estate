// agent-tui#26: two genuinely different answers to "what is this screen
// for", delivered through the same cycler mechanism lane.Variants already
// proves works (a numbered, live-against-real-data picker; see variants.go's
// own doc comment) -- not two colour schemes of one layout, and not a single
// opinionated redesign. Jon's own standing directive, quoted in the issue:
// "DO NOT ASK HIM WHICH -- build both readings and let him cycle."
//
// readings[0] is the default -- lane.Variants[0]/groupStyles[0]'s own rule
// applied a third time: silence still yields something sane.
package rail

var readings = []readingDef{workReading, statusReading}

type readingDef struct {
	ID          string
	Name        string
	Description string
}

// workReading answers "what is in flight, and how long has it been open" --
// the task each lane is actually working, its own age, then the lane's live
// state as context.
var workReading = readingDef{
	ID:          "work",
	Name:        "Work",
	Description: "task per lane, how long it's been open",
}

// statusReading answers "what is healthy, what needs attention" -- the
// lane's state read first as a health question, "needs human" surfaced
// explicitly with why, age folded in as "since" rather than "open" (the same
// duration, a different question asked of it).
var statusReading = readingDef{
	ID:          "status",
	Name:        "Status",
	Description: "health, needs-human flag and why, since when",
}
