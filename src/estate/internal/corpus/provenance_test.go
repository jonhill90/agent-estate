package corpus

import "testing"

// A parameter that asserts an obligation the prompt never contains was written
// by the itemiser, not the operator. That is the exact defect that put
// "~/.local holds state like logs and status" into the record as law.
func TestInventedObligationIsDetected(t *testing.T) {
	pw, sw := words("~/.local holds state like logs and status, never scripts"),
		words("dude i dont understand, you have a dir full of scripts in a .local folder, this is cheating")
	hit := 0
	for w := range pw {
		if sw[w] {
			hit++
		}
	}
	overlap := float64(hit) / float64(len(pw))
	if overlap > 0.5 {
		t.Fatalf("overlap %.2f is too high; this pair must read as poorly supported", overlap)
	}
}

func TestFaithfulParameterScoresHigh(t *testing.T) {
	pw, sw := words("deploys go through the pipeline only"),
		words("deploys go through the pipeline only, do not hand-fix on the host")
	hit := 0
	for w := range pw {
		if sw[w] {
			hit++
		}
	}
	if got := float64(hit) / float64(len(pw)); got < 0.9 {
		t.Fatalf("a faithful distillation scored %.2f; it must score near 1", got)
	}
}

// Zero findings from a corpus that exists is blindness, not a clean bill.
func TestAuditRefusesToReportEmptyAsClean(t *testing.T) {
	t.Setenv("ESTATE_CORPUS", t.TempDir()+"/absent.sqlite3")
	if _, err := Audit(); err == nil {
		t.Fatal("Audit() returned nil error for an unreadable corpus")
	}
}
