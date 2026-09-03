package knowledge

import (
	"testing"
	"time"
)

func TestNextIDIsFourteenChars(t *testing.T) {
	c := newIDClock(time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC))
	if id := c.NextID(); len(id) != 14 {
		t.Fatalf("NextID() = %q, want 14 chars", id)
	}
}

// TestNextIDsGeneratedInTheSameSecondNeverCollide is the whole reason the
// scheme exists -- Jon's own stated purpose ("prevents agent collision").
func TestNextIDsGeneratedInTheSameSecondNeverCollide(t *testing.T) {
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	c := newIDClock(base)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := c.NextID()
		if seen[id] {
			t.Fatalf("id %q repeated at item %d", id, i)
		}
		seen[id] = true
	}
}

func TestNextIDsAreOneSecondApartInOrder(t *testing.T) {
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	c := newIDClock(base)
	first := c.NextID()
	second := c.NextID()
	want := base.Add(time.Second).Format(idLayout)
	if second != want {
		t.Fatalf("second id = %q, want %q (one second after first %q)", second, want, first)
	}
}
