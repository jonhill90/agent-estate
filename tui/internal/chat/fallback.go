package chat

import "errors"

// FallbackSource is the Source cmd/keelson actually wires in: try real
// first, and only reach for the fixture when real reports itself
// UNCONFIGURED (ErrNoProjectDir, or any Source's own equivalent -- see
// Threads below). This is the one place in the package that decides
// which of the two the operator is looking at, and it must get that
// decision right in both directions (agent-b3.md's own bar):
//
//   - real configured, returns threads (even zero of them) with no error:
//     use real's own answer, unmodified. Real emptiness must never be
//     replaced with fixture content.
//   - real configured but hit a genuine error (a file exists and could
//     not be read/parsed): return that error to the caller as-is. A read
//     failure is not the same thing as "nothing is configured," and
//     papering over it with the fixture would hide a real problem behind
//     content that looks fine.
//   - real reports it is simply not configured (no project directory
//     exists at all -- the one condition this type treats as "there is
//     no real source here"): fall back to the fixture, with every
//     returned thread tagged Fixture: true (FixtureSource's own doc
//     comment) so the view can render a visible notice.
type FallbackSource struct {
	real    Source
	fixture Source
}

// NewFallbackSource wraps real with FixtureSource as the fallback. real
// may be nil (no real Source could even be constructed, e.g. no
// projectsDir could be resolved at all) -- treated exactly like a real
// Source that always returns ErrNoProjectDir.
func NewFallbackSource(real Source) FallbackSource {
	return FallbackSource{real: real, fixture: NewFixtureSource()}
}

func (s FallbackSource) Threads() ([]Thread, error) {
	if s.real != nil {
		threads, err := s.real.Threads()
		if !errors.Is(err, ErrNoProjectDir) {
			// Includes the nil-error/empty-slice case (real ran fine,
			// found nothing) and the genuine-error case (real ran and
			// failed) -- both are real's own honest answer and must reach
			// the caller unchanged, never swapped for the fixture.
			return threads, err
		}
	}
	return s.fixture.Threads()
}

var _ Source = FallbackSource{}
