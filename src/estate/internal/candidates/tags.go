package candidates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/notemeta"
)

// TagNotes adds reviewed associative metadata without changing note meaning.
// Keys are vault-relative canonical note paths; values are governed flat tags.
// The complete batch is validated before writeSet backs up and writes anything.
func TagNotes(vault string, batch map[string][]string, apply bool) (int, error) {
	unlock, e := lockFile(filepath.Join(vault, "99 - Meta/.candidate-memory.lock"))
	if e != nil {
		return 0, e
	}
	defer unlock()
	vocab, e := os.ReadFile(filepath.Join(vault, "99 - Meta/tags.md"))
	if e != nil {
		return 0, e
	}
	changes := map[string][]byte{}
	for rel, additions := range batch {
		if filepath.IsAbs(rel) || filepath.Clean(rel) != rel || !strings.HasPrefix(rel, "01 - Notes/") || !regexp.MustCompile(`^\d{12}(\d{2})?\.md$`).MatchString(filepath.Base(rel)) {
			return 0, fmt.Errorf("invalid note path %q", rel)
		}
		path := filepath.Join(vault, rel)
		raw, e := os.ReadFile(path)
		if e != nil {
			return 0, e
		}
		if field(string(raw), "id") != strings.TrimSuffix(filepath.Base(path), ".md") || field(string(raw), "type") == "" {
			return 0, fmt.Errorf("invalid note identity/schema: %s", rel)
		}
		// Reviewed publications have a persisted full-file receipt. Until tag
		// edits update that receipt transactionally, refuse instead of stranding
		// their subsequent accept/reject operation behind an external-edit guard.
		if field(string(raw), "candidate_id") != "" {
			return 0, fmt.Errorf("reviewed publication requires receipt-aware tag update: %s", rel)
		}
		tags, e := notemeta.Tags(string(raw))
		if e != nil {
			return 0, e
		}
		seen := map[string]bool{}
		for _, t := range tags {
			seen[t] = true
		}
		changed := false
		for _, t := range additions {
			if !regexp.MustCompile(`^[a-z]+(?:-[a-z0-9]+)*$`).MatchString(t) || !strings.Contains(string(vocab), "`"+t+"`") {
				return 0, fmt.Errorf("ungoverned flat tag %q", t)
			}
			if !seen[t] {
				tags = append(tags, t)
				seen[t] = true
				changed = true
			}
		}
		if !changed {
			continue
		}
		sort.Strings(tags)
		encoded, _ := json.Marshal(tags)
		next := notemeta.SetTags(string(raw), string(encoded))
		next = replaceField(next, "updated", time.Now().UTC().Format(time.RFC3339))
		changes[path] = []byte(next)
	}
	if apply && len(changes) > 0 {
		return len(changes), writeSet(vault, changes)
	}
	return len(changes), nil
}

// ExtendTags records a bounded, operator-authorized vocabulary extension.
func ExtendTags(vault string, additions map[string]string, apply bool) (int, error) {
	unlock, e := lockFile(filepath.Join(vault, "99 - Meta/.candidate-memory.lock"))
	if e != nil {
		return 0, e
	}
	defer unlock()
	path := filepath.Join(vault, "99 - Meta/tags.md")
	raw, e := os.ReadFile(path)
	if e != nil {
		return 0, e
	}
	var keys []string
	for tag, meaning := range additions {
		if !regexp.MustCompile(`^[a-z]+(?:-[a-z0-9]+)*$`).MatchString(tag) || strings.TrimSpace(meaning) == "" || strings.ContainsAny(meaning, "\n\r|`") {
			return 0, fmt.Errorf("invalid vocabulary entry %q", tag)
		}
		if !strings.Contains(string(raw), "`"+tag+"`") {
			keys = append(keys, tag)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return 0, nil
	}
	next := string(raw) + "\n## Associative vocabulary — Push 4.5\n\nFlat topical tags, authorized by the Push 4.5 brief; type remains frontmatter.\n\n| Value | Association |\n|---|---|\n"
	for _, k := range keys {
		next += "| `" + k + "` | " + additions[k] + " |\n"
	}
	next = replaceField(next, "updated", time.Now().UTC().Format(time.RFC3339))
	if apply {
		return len(keys), writeSet(vault, map[string][]byte{path: []byte(next)})
	}
	return len(keys), nil
}
