// Package notemeta reads the restricted tag lists used by governed vault notes.
package notemeta

import (
	"fmt"
	"regexp"
	"strings"
)

var tagWord = regexp.MustCompile(`^[a-z0-9]+(?:[-/][a-z0-9]+)*$`)

// Tags accepts JSON/YAML flow lists and YAML block lists, never body text.
func Tags(raw string) ([]string, error) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("missing frontmatter")
	}
	var tags []string
	active := false
	add := func(s string) error {
		s = strings.Trim(strings.TrimSpace(s), "\"'")
		if !tagWord.MatchString(s) {
			return fmt.Errorf("invalid tag %q", s)
		}
		for _, t := range tags {
			if t == s {
				return nil
			}
		}
		tags = append(tags, s)
		return nil
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return tags, nil
		}
		if strings.HasPrefix(line, "tags:") {
			active = true
			value := strings.TrimSpace(strings.TrimPrefix(line, "tags:"))
			if value == "" {
				continue
			}
			if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
				return nil, fmt.Errorf("tags must be a list")
			}
			value = strings.TrimSpace(value[1 : len(value)-1])
			if value != "" {
				for _, t := range strings.Split(value, ",") {
					if e := add(t); e != nil {
						return nil, e
					}
				}
			}
			active = false
		} else if active {
			s := strings.TrimSpace(line)
			if strings.HasPrefix(s, "- ") {
				if e := add(strings.TrimPrefix(s, "- ")); e != nil {
					return nil, e
				}
			} else if s != "" {
				active = false
			}
		}
	}
	return nil, fmt.Errorf("unclosed frontmatter")
}

// SetTags changes only frontmatter; a body example beginning tags: is evidence.
func SetTags(raw, encoded string) string {
	if !strings.HasPrefix(raw, "---\n") {
		return raw
	}
	end := strings.Index(raw[3:], "\n---") + 3
	if end < 3 {
		return raw
	}
	front, body := raw[:end], raw[end:]
	re := regexp.MustCompile(`(?m)^tags:[^\n]*(?:\n[ \t]*-[^\n]*)*`)
	if re.MatchString(front) {
		front = re.ReplaceAllString(front, "tags: "+encoded)
	} else {
		front += "\ntags: " + encoded
	}
	return front + body
}
