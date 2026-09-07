package notemeta

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Merge preserves associative tags and the Relations section. Projection-owned
// structural/time tags regenerate, so withdrawal cannot retain standing-rule.
func Merge(generated, previous string) (string, error) {
	if previous == "" {
		previous = "---\n---\n"
	}
	tags, e := Tags(generated)
	if e != nil {
		return "", e
	}
	old, e := Tags(previous)
	if e != nil {
		return "", e
	}
	seen := map[string]bool{}
	for _, t := range tags {
		seen[t] = true
	}
	for _, t := range old {
		if t == "note" || t == "standing-rule" || regexp.MustCompile(`^\d{2}-\d{4}$`).MatchString(t) {
			continue
		}
		if !seen[t] {
			tags = append(tags, t)
			seen[t] = true
		}
	}
	sort.Strings(tags)
	b, _ := json.Marshal(tags)
	generated = SetTags(generated, string(b))
	if start := strings.Index(previous, "\n## Relations\n"); start >= 0 {
		section := previous[start:]
		if end := strings.Index(section[1:], "\n## "); end >= 0 {
			section = section[:end+1]
		}
		generated = strings.TrimRight(generated, "\n") + "\n" + section
	}
	return generated, nil
}
