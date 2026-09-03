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

// writeManyColorPNG writes a PNG with exactly n distinct colors, for n
// larger than writeMultiColorPNG can produce -- writeMultiColorPNG derives
// each color from x%256 arithmetic, which repeats with period 256 and so
// tops out well under a few hundred truly distinct colors regardless of n.
// This encodes each color from a plain incrementing index across all three
// channels (a bijection up to 16,777,216), so a caller asking for
// thousands of colors -- the range agent-estate#956's measured settled frames
// (4393, 5674) actually live in -- gets exactly that many.
func writeManyColorPNG(t *testing.T, path string, n int) {
	t.Helper()
	const w = 128
	h := (n + w - 1) / w
	if h < 1 {
		h = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var last color.RGBA
	for i := 0; i < w*h; i++ {
		x, y := i%w, i/w
		c := last
		if i < n {
			c = color.RGBA{R: uint8(i), G: uint8(i >> 8), B: uint8(i >> 16), A: 255}
			last = c
		}
		img.Set(x, y, c)
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

	report, err := RunUntilSettled([]string{target}, UniformFloor(2), 8, runTape)
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

	report, err := RunUntilSettled([]string{target}, UniformFloor(2), 4, runTape)
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

	if _, err := RunUntilSettled([]string{target}, UniformFloor(2), 1, runTape); err != nil {
		t.Fatalf("RunUntilSettled: %v", err)
	}
	if sawFileAtRunTapeStart {
		t.Fatalf("stale file from a previous run was still present when runTape started")
	}
}

// defaultMinColors mirrors cmd/vhscapture's own -min-colors default. It is
// duplicated here deliberately rather than imported (main.go is package
// main) -- if the CLI's default ever drifts from this value without this
// test being updated too, that drift is the bug this test exists to catch.
const defaultMinColors = 1000

// TestRunUntilSettledRejectsPartialFrameAtDefaultMinColors is agent-estate#956's
// review, reproduced as a red-before/green-after mutation check: a
// synthetic 259-colour PNG -- the exact partial/transitional shape both
// agent-estate#947 and this PR's own body name as not-a-real-capture -- must be
// rejected at the tool's own default threshold, with no flag needed to get
// that behaviour. Before this fix (-min-colors defaulted to 2) this exact
// frame passed on the first attempt; at defaultMinColors it must exhaust
// every attempt and report failure instead.
func TestRunUntilSettledRejectsPartialFrameAtDefaultMinColors(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shot.png")
	runTape := func() error {
		writeMultiColorPNG(t, target, 259) // agent-estate#956's own adversarial case
		return nil
	}

	report, err := RunUntilSettled([]string{target}, UniformFloor(defaultMinColors), 4, runTape)
	if err == nil {
		t.Fatalf("RunUntilSettled: want error for a 259-colour partial frame at the default min-colors, got nil (settled=%v)", report.Settled)
	}
	if report.Settled {
		t.Fatalf("report.Settled = true, want false -- a 259-colour frame must not count as settled at the default")
	}
	for _, a := range report.Attempts {
		if a.Passed {
			t.Fatalf("attempt %d passed verification against a 259-colour frame at min-colors=%d; want every attempt to fail", a.N, defaultMinColors)
		}
	}
}

// TestRunUntilSettledAcceptsSettledFrameAtDefaultMinColors is the other
// mutation direction: a frame at the low end of the two measured real
// settled counts (4393 and 5674 colours, agents-mode.tape) must still pass
// at the default -- the raised default must not be so high it rejects a
// genuine capture.
func TestRunUntilSettledAcceptsSettledFrameAtDefaultMinColors(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shot.png")
	runTape := func() error {
		writeManyColorPNG(t, target, 4393) // the lower of the two measured settled counts
		return nil
	}

	report, err := RunUntilSettled([]string{target}, UniformFloor(defaultMinColors), 4, runTape)
	if err != nil {
		t.Fatalf("RunUntilSettled: %v (want a 4393-colour settled frame to pass at the default min-colors)", err)
	}
	if !report.Settled {
		t.Fatalf("report.Settled = false, want true")
	}
	if len(report.Attempts) != 1 {
		t.Fatalf("len(report.Attempts) = %d, want 1 (should settle on the first attempt)", len(report.Attempts))
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
	report, err := RunUntilSettled([]string{target}, UniformFloor(2), 8, runTape)
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
