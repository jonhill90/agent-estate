package vhscapture

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PerTargetFloors is agent-estate#960's fix for the finding that killed
// its predecessor: -min-colors' own default (1000, agent-estate#956) was
// measured against two tapes and is wrong for over a third of this
// repo's real tapes, because the number that separates a torn/partial
// frame from a genuine settled one is not the same across tapes. A
// sparse pane (a text list on a dark background, e.g.
// testdata/vhs/knowledge-index.tape) settles at a real, correct few
// hundred colours; a busier pane's own torn/partial frame
// (testdata/vhs/agents-mode.tape, measured directly: 263 colours) can
// outscore it. No constant is above one and below the other.
//
// The fix measured here is a floor recorded NEXT TO THE TAPE, from that
// tape's own real settled colour count, not borrowed from a different
// tape's evidence (agent-estate#960's own preferred outcome #2). This
// function reads that sidecar file, if one exists, and returns a
// basename -> floor map for the tape's own Screenshot targets.
//
// Sidecar path: the tape's own path with ".tape" replaced by
// ".mincolors" (testdata/vhs/knowledge-index.tape ->
// testdata/vhs/knowledge-index.mincolors). Format: one
// "<screenshot-basename>=<int>" assignment per real line; blank lines
// and lines starting with "#" (comments recording how the number was
// measured, and when) are ignored. Keyed by basename, not the full
// target path, because every tape in this repo writes its Screenshots
// into the same testdata/vhs/out/ directory and a basename is unique
// within one tape's own target list, never across tapes -- there is no
// need for the full path.
//
// Absence of a sidecar file is normal, not an error: it means this tape
// has not been measured yet and every one of its targets falls back to
// the caller's own default floor. Returns a nil map and nil error in
// that case.
func PerTargetFloors(tapePath string) (map[string]int, error) {
	sidecar := strings.TrimSuffix(tapePath, filepath.Ext(tapePath)) + ".mincolors"
	f, err := os.Open(sidecar)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	floors := map[string]int{}
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, val, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: want \"<basename>=<int>\", got %q", sidecar, line, text)
		}
		key = strings.TrimSpace(key)
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", sidecar, line, err)
		}
		floors[key] = n
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return floors, nil
}

// SidecarPath returns the sidecar file PerTargetFloors would read for
// tapePath, whether or not it actually exists -- used to report which
// file a floor did (or would) come from.
func SidecarPath(tapePath string) string {
	return strings.TrimSuffix(tapePath, filepath.Ext(tapePath)) + ".mincolors"
}
