package hookstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// settingsShape is the one part of a Claude settings.json this package
// reads -- deliberately narrow (a full schema lives nowhere central to
// depend on, and every other key here is inert to this question).
type settingsShape struct {
	Hooks struct {
		PreToolUse []struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"PreToolUse"`
	} `json:"hooks"`
}

func parseSettingsHookCommands(b []byte) ([]string, error) {
	var s settingsShape
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	var commands []string
	for _, matcher := range s.Hooks.PreToolUse {
		for _, h := range matcher.Hooks {
			if h.Command != "" {
				commands = append(commands, h.Command)
			}
		}
	}
	return commands, nil
}

// wiredPaths reads the LIVE Claude settings file (settingsPath, typically
// ~/.claude/settings.json) and returns the set of hooks/-relative paths it
// registers as PreToolUse hooks -- but ONLY those whose command resolves
// inside checkoutDir; a hook wired from anywhere else is a different
// finding (a second deployment surface), not this package's to silently
// fold in.
func wiredPaths(settingsPath, checkoutDir string) (map[string]bool, error) {
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, err
	}
	commands, err := parseSettingsHookCommands(b)
	if err != nil {
		return nil, err
	}
	return relativeToCheckout(commands, checkoutDir), nil
}

// parseSettingsHookPaths reads origin/main's OWN settings fragment
// (settings/claude/settings.json), whose "command" values are already
// checkout-relative ("hooks/foo.sh"), never absolute -- see #357's own
// fragment for the shape this reads.
func parseSettingsHookPaths(b []byte) (map[string]bool, error) {
	commands, err := parseSettingsHookCommands(b)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, c := range commands {
		set[strings.TrimPrefix(filepath.ToSlash(c), "./")] = true
	}
	return set, nil
}

// relativeToCheckout keeps only the commands that live inside checkoutDir
// and reduces each to its checkoutDir-relative, slash-form path.
func relativeToCheckout(commands []string, checkoutDir string) map[string]bool {
	set := map[string]bool{}
	prefix := strings.TrimSuffix(checkoutDir, "/") + "/"
	for _, c := range commands {
		if !strings.HasPrefix(c, prefix) {
			continue
		}
		set[strings.TrimPrefix(c, prefix)] = true
	}
	return set
}
