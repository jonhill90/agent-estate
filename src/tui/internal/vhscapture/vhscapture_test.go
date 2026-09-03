package vhscapture

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScreenshotTargetsFindsEveryTarget(t *testing.T) {
	tape := "Hide\nType \"go build\" Enter\nShow\n" +
		"Wait+Screen /Home/\n" +
		"Screenshot testdata/vhs/out/one.png\n" +
		"Type \"g\"\n" +
		"Screenshot testdata/vhs/out/two.png\n"
	got := ScreenshotTargets(tape)
	want := []string{"testdata/vhs/out/one.png", "testdata/vhs/out/two.png"}
	if len(got) != len(want) {
		t.Fatalf("ScreenshotTargets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ScreenshotTargets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestScreenshotTargetsIgnoresProseThatMentionsTheWord(t *testing.T) {
	tape := "# Do not confuse this comment's own mention of Screenshot with a\n" +
		"# real Screenshot command -- it has no leading 'Screenshot <path>' line.\n"
	got := ScreenshotTargets(tape)
	if len(got) != 0 {
		t.Fatalf("ScreenshotTargets = %v, want none (prose only)", got)
	}
}

// writeSolidPNG writes a single-color PNG -- the shape agent-estate#947's own review
// found for a genuinely blank capture ("1 distinct color, (23,23,23)
// everywhere").
func writeSolidPNG(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

// writeMultiColorPNG writes a PNG with n distinct colors -- standing in
// for a genuinely settled, content-rich frame.
func writeMultiColorPNG(t *testing.T, path string, n int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, n, 1))
	for x := 0; x < n; x++ {
		img.Set(x, 0, color.RGBA{R: uint8(x % 256), G: uint8((x * 7) % 256), B: uint8((x * 13) % 256), A: 255})
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

func TestCountColorsDistinguishesBlankFromReal(t *testing.T) {
	dir := t.TempDir()
	blank := filepath.Join(dir, "blank.png")
	real := filepath.Join(dir, "real.png")
	writeSolidPNG(t, blank, color.RGBA{R: 23, G: 23, B: 23, A: 255})
	writeMultiColorPNG(t, real, 200)

	blankColors, err := CountColors(blank)
	if err != nil {
		t.Fatalf("CountColors(blank): %v", err)
	}
	if blankColors != 1 {
		t.Fatalf("CountColors(blank) = %d, want 1", blankColors)
	}

	realColors, err := CountColors(real)
	if err != nil {
		t.Fatalf("CountColors(real): %v", err)
	}
	if realColors != 200 {
		t.Fatalf("CountColors(real) = %d, want 200", realColors)
	}
}

// TestRunUntilSettledRetriesPastBlankAndMissingFrames proves the guard
// by construction, the same style vhscheck's own tests use: attempts 1
// and 2 reproduce agent-estate#947's exact failure shapes (a solid-color blank frame,
// then a missing file entirely), attempt 3 is a genuine settled frame --
// RunUntilSettled must retry through both bad attempts and stop on the
// third.
func TestRunUntilSettledRetriesPastBlankAndMissingFrames(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shot.png")
	attempt := 0
	runTape := func() error {
		attempt++
		switch attempt {
		case 1:
			writeSolidPNG(t, target, color.RGBA{R: 23, G: 23, B: 23, A: 255}) // blank
		case 2:
			// missing: vhs "succeeded" but wrote nothing
		case 3:
			writeMultiColorPNG(t, target, 200) // settled
		}
		return nil
	}

	report, err := RunUntilSettled([]string{target}, 2, 8, runTape)
	if err != nil {
		t.Fatalf("RunUntilSettled: %v", err)
	}
	if !report.Settled {
		t.Fatalf("report.Settled = false, want true")
	}
	if len(report.Attempts) != 3 {
		t.Fatalf("len(report.Attempts) = %d, want 3 (should have stopped at the first settled attempt)", len(report.Attempts))
	}
	if report.Attempts[0].Passed || report.Attempts[1].Passed {
		t.Fatalf("attempts 1 and 2 should both have failed verification: %+v", report.Attempts[:2])
	}
	if !report.Attempts[2].Passed {
		t.Fatalf("attempt 3 should have passed verification: %+v", report.Attempts[2])
	}
}

// TestRunUntilSettledExhaustsAttemptsAndReportsFailure is the other
// mutation-check direction: every attempt stays blank, so
// RunUntilSettled must give up after maxAttempts and return a non-nil
// error rather than falsely claiming success.
func TestRunUntilSettledExhaustsAttemptsAndReportsFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shot.png")
	runTape := func() error {
		writeSolidPNG(t, target, color.RGBA{R: 23, G: 23, B: 23, A: 255})
		return nil
	}

	report, err := RunUntilSettled([]string{target}, 2, 4, runTape)
	if err == nil {
		t.Fatalf("RunUntilSettled: want error when every attempt stays blank, got nil")
	}
	if report.Settled {
		t.Fatalf("report.Settled = true, want false")
	}
	if len(report.Attempts) != 4 {
		t.Fatalf("len(report.Attempts) = %d, want 4 (maxAttempts)", len(report.Attempts))
	}
}

func TestRunUntilSettledRemovesStaleFileBeforeEachAttempt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shot.png")
	// Pre-seed a stale settled-looking file from a "previous run" --
	// RunUntilSettled must not treat this leftover as attempt 1's own
	// output; it must delete it before calling runTape at all.
	writeMultiColorPNG(t, target, 200)

	var sawFileAtRunTapeStart bool
	runTape := func() error {
		if _, err := os.Stat(target); err == nil {
			sawFileAtRunTapeStart = true
		}
		writeMultiColorPNG(t, target, 200)
		return nil
	}

	if _, err := RunUntilSettled([]string{target}, 2, 1, runTape); err != nil {
		t.Fatalf("RunUntilSettled: %v", err)
	}
	if sawFileAtRunTapeStart {
		t.Fatalf("stale file from a previous run was still present when runTape started")
	}
}

func TestReportPrintShowsEveryAttemptNotJustTheLast(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shot.png")
	attempt := 0
	runTape := func() error {
		attempt++
		if attempt < 3 {
			writeSolidPNG(t, target, color.RGBA{R: 23, G: 23, B: 23, A: 255})
		} else {
			writeMultiColorPNG(t, target, 200)
		}
		return nil
	}
	report, err := RunUntilSettled([]string{target}, 2, 8, runTape)
	if err != nil {
		t.Fatalf("RunUntilSettled: %v", err)
	}
	var sb strings.Builder
	report.Print(&sb)
	out := sb.String()
	for _, want := range []string{"attempt 1", "attempt 2", "attempt 3", "settled: yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Report.Print output missing %q; got:\n%s", want, out)
		}
	}
}
