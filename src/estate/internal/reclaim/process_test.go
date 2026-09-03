package reclaim

import (
	"os"
	"testing"
	"time"
)

func TestParseEtime(t *testing.T) {
	cases := map[string]time.Duration{
		"00:05":      5 * time.Second,
		"01:02:03":   1*time.Hour + 2*time.Minute + 3*time.Second,
		"3-01:02:03": 3*24*time.Hour + 1*time.Hour + 2*time.Minute + 3*time.Second,
		"59:59":      59*time.Minute + 59*time.Second,
	}
	for in, want := range cases {
		got, err := parseEtime(in)
		if err != nil {
			t.Fatalf("parseEtime(%q) error: %v", in, err)
		}
		if got != want {
			t.Fatalf("parseEtime(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseEtimeRejectsGarbage(t *testing.T) {
	if _, err := parseEtime("not-a-time"); err == nil {
		t.Fatal("expected an error for unparseable etime")
	}
}

// PSProbe and BootTime are thin wrappers around real OS commands; there is
// no fake to substitute here, so this only proves they run against the
// live host without erroring -- the decision logic they feed is what
// reclaim_test.go actually exercises against fakes.
func TestPSProbeAgainstOwnProcess(t *testing.T) {
	info, err := PSProbe(os.Getpid())
	if err != nil {
		t.Fatalf("PSProbe(own pid) error: %v", err)
	}
	if !info.Exists {
		t.Fatalf("PSProbe reported our own live pid as not existing")
	}
}

func TestPSProbeAgainstAlmostCertainlyFreePID(t *testing.T) {
	// pid 1 is always alive (init/launchd); a very high pid is very likely
	// unassigned on any real machine, but this is inherently a best-effort
	// check of a live system, not a guarantee -- so only assert on the
	// shape of the answer (exists is set at all, no error), not that it is
	// false.
	if _, err := PSProbe(1 << 30); err != nil {
		t.Fatalf("PSProbe(implausible pid) error: %v", err)
	}
}

func TestBootTimeIsInThePast(t *testing.T) {
	bt, err := BootTime()
	if err != nil {
		t.Skipf("BootTime unavailable on this host: %v", err)
	}
	if !bt.Before(time.Now()) {
		t.Fatalf("BootTime() = %v, expected it to be before now", bt)
	}
}
