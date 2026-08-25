package agent

import "context"

// Adapter is the seam between dispatch and one specific vendor CLI. Every
// implementation drives its vendor's CLI as a subprocess and proves delivery
// from what the process actually returned -- a parsed result or a typed
// error, never a scraped screen. See claude.go's own doc comment for why
// that distinction is the entire reason this daemon exists.
//
// Jon's constraint, from the corpus this repo tracks: the meta-harness must
// be able to drive claude code OR codex OR copilot OR pi OR a raw API token
// per agent, because driving the vendor CLI is what makes a subscription's
// tokens usable instead of paying per-token API rates -- that is the whole
// point of this package existing at all, not an incidental abstraction.
//
// Adapter is deliberately the same three-line shape Claude already had
// before this existed (Run takes a prompt, returns Claude's own Result
// shape and an error) -- adding the interface did not change Claude's own
// method signature at all. A future adapter whose vendor CLI cannot fill
// every Result field (Codex has no per-turn USD figure -- see codex.go's
// own doc comment) leaves the field at its zero value rather than
// fabricating one; callers reading that field already have to treat "0"
// as "nothing recorded", the same posture dispatch.go's budget gate takes
// today.
type Adapter interface {
	Run(ctx context.Context, prompt string) (*Result, error)
}

// Compile-time proof each adapter actually satisfies the interface it is
// dispatched behind -- caught here, not at the first RunGated call that
// tries to pass one.
var (
	_ Adapter = (*Claude)(nil)
	_ Adapter = (*Codex)(nil)
)
