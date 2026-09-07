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
)

func inmaps(vault string) bool {
	st, e := os.Stat(filepath.Join(vault, "01 - Notes"))
	return e == nil && st.IsDir()
}
func scalar(s string) string { b, _ := json.Marshal(s); return string(b) }
func field(raw, key string) string {
	for _, l := range strings.Split(raw, "\n") {
		if strings.HasPrefix(l, key+": ") {
			v := strings.TrimPrefix(l, key+": ")
			var s string
			if json.Unmarshal([]byte(v), &s) == nil {
				return s
			}
			return v
		}
	}
	return ""
}
func replaceField(raw, key, value string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:.*$`)
	if re.MatchString(raw) {
		return re.ReplaceAllStringFunc(raw, func(string) string { return key + ": " + value })
	}
	return strings.Replace(raw, "---\n", "---\n"+key+": "+value+"\n", 1)
}
func validateINMAPS(vault string, p Proposal) error {
	if !regexp.MustCompile(`^(Fact|Thought|Question|Parameter|Research|MOC|Project|Source|user|feedback|project|reference)$`).MatchString(p.Type) {
		return fmt.Errorf("invalid INMAPS type")
	}
	if strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Description) == "" || strings.TrimSpace(p.Learning) == "" {
		return fmt.Errorf("title, description and learning required")
	}
	vocab, e := os.ReadFile(filepath.Join(vault, "99 - Meta", "tags.md"))
	if e != nil {
		return e
	}
	if len(p.Tags) == 0 {
		return fmt.Errorf("at least one governed tag required")
	}
	for _, tag := range p.Tags {
		// A tag is either one of the closed namespaced axes
		// (kind/lifecycle/project/topic -- unchanged, per 99 - Meta/tags.md)
		// OR a flat lowercase word with no slash at all -- inmaps-spec
		// section 3's own small exception for generated Source-type
		// records (e.g. "source"), which never carry a kind value as a
		// tag since kind already has its own dedicated frontmatter field.
		if !regexp.MustCompile(`^(kind|lifecycle|project|topic)/[a-z0-9]+(?:-[a-z0-9]+)*$|^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(tag) {
			return fmt.Errorf("invalid tag %q", tag)
		}
		if strings.HasPrefix(tag, "topic/") {
			continue
		}
		if strings.HasPrefix(tag, "project/") {
			if _, e := os.Stat(filepath.Join(vault, "04 - Projects", strings.TrimPrefix(tag, "project/")+".md")); e == nil {
				continue
			}
		}
		if !strings.Contains(string(vocab), "`"+tag+"`") {
			return fmt.Errorf("tag outside vocabulary: %s", tag)
		}
	}
	return nil
}

// writeSet backs up every existing target before writing. A returned write error
// restores the complete set; the saved review permits retry after process loss.
//
// logPath moved from agent/log.md to 99 - Meta/log.md under the agent/
// dissolution (run/inmaps-spec.md §7b, P5 batch 1) -- repointed in the same
// change that moved the file, per that section's own binding rule (never
// move content ahead of its readers).
func writeSet(vault string, changes map[string][]byte) error {
	logPath := filepath.Join(vault, "99 - Meta/log.md")
	if _, ok := changes[logPath]; !ok {
		previous, e := os.ReadFile(logPath)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		var names []string
		for p := range changes {
			rel, e := filepath.Rel(vault, p)
			if e != nil {
				return e
			}
			names = append(names, rel)
		}
		sort.Strings(names)
		at := time.Now().UTC().Format(time.RFC3339)
		changes[logPath] = []byte("## " + at[:10] + "\n\n**Update** process:estate-candidates — " + strings.Join(names, ", ") + " (" + at + ")\n\n" + string(previous))
	}
	backup, e := os.MkdirTemp(filepath.Join(vault, "agent"), ".inmaps-backup-")
	if e != nil {
		return e
	}
	old := map[string][]byte{}
	var keys []string
	for p := range changes {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for i, p := range keys {
		rel, e := filepath.Rel(vault, p)
		if e != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path outside vault")
		}
		// Refuse symlink parents, including existing ancestors of new targets.
		for q := p; q != vault; q = filepath.Dir(q) {
			st, e := os.Lstat(q)
			if e == nil && st.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink target: %s", q)
			}
			if e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		b, e := os.ReadFile(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		old[p] = b
		if b != nil {
			bp := filepath.Join(backup, fmt.Sprint(i))
			if e = os.WriteFile(bp, b, 0600); e != nil {
				return e
			}
			c, e := os.ReadFile(bp)
			if e != nil || digest(string(c)) != digest(string(b)) {
				return fmt.Errorf("backup checksum failed")
			}
		}
	}
	for _, p := range keys {
		b := changes[p]
		if string(b) == string(old[p]) {
			continue
		}
		if e = os.MkdirAll(filepath.Dir(p), 0700); e == nil {
			e = atomicMemoryWrite(p, b)
		}
		if e != nil {
			for _, q := range keys {
				if old[q] == nil {
					os.Remove(q)
				} else {
					atomicMemoryWrite(q, old[q])
				}
			}
			return fmt.Errorf("write failed; restored backup %s: %w", backup, e)
		}
	}
	return nil
}
func noteBytes(id, noteID, status string, r MemoryReview, at string) []byte {
	p := r.Proposal
	typ := p.Type
	if typ == "user" || typ == "feedback" || typ == "project" || typ == "reference" {
		typ = "Fact"
	}
	tags, _ := json.Marshal(p.Tags)
	result := []byte(fmt.Sprintf("---\ntype: %s\nid: %s\ntitle: %s\ndescription: %s\ntags: %s\ncreated: %s\nupdated: %s\nstatus: %s\nsource: %s\nsources: [{id: %s, resource: %s}]\ngenerated: {by: process:estate-candidates, at: %s}\nverified: [{by: %s, at: %s}]\ncandidate_id: %s\nmemory_revision: %s\nmemory_status: %s\nsupersedes: %s\n---\n\n# %s\n\n%s\n\nOperator context (reviewed paraphrase): %s\n\nAssistant context (not operator instruction): %s\n", typ, scalar(noteID), scalar(p.Title), scalar(p.Description), tags, at, at, status, scalar(r.Citation), scalar(id), scalar(r.Citation), at, scalar(p.Reviewer), at, id, r.Revision, map[string]string{"draft": "proposed", "stable": "promoted", "deprecated": "rejected"}[status], scalar(p.Supersedes), p.Title, p.Learning, p.OperatorContext, p.AssistantContext))
	if status == "draft" {
		result = []byte(regexp.MustCompile(`(?m)^verified:.*\n`).ReplaceAllString(string(result), ""))
	}
	return result
}
func draftPath(vault, id, revision string) string {
	return filepath.Join(vault, "00 - Inbox", "candidate-"+digest(id + revision)[:24]+".md")
}

// StageMemory is the mandatory airlock for INMAPS publication. Legacy vaults
// remain readable; CLI writes to INMAPS always validate vocabulary first.
func StageMemory(vault, id string, r MemoryReview, apply bool) error {
	if !inmaps(vault) || r.Proposal.DestinationKind == "repo" {
		return nil
	}
	if e := validateINMAPS(vault, r.Proposal); e != nil {
		return e
	}
	if !apply {
		return nil
	}
	unlock, e := lockFile(filepath.Join(vault, "agent/.candidate-memory.lock"))
	if e != nil {
		return e
	}
	defer unlock()
	path := draftPath(vault, id, r.Revision)
	if b, e := os.ReadFile(path); e == nil {
		if field(string(b), "memory_revision") == r.Revision {
			return nil
		}
		return fmt.Errorf("draft collision")
	}
	return writeSet(vault, map[string][]byte{path: noteBytes(id, "candidate-"+digest(id + r.Revision)[:24], "draft", r, time.Now().UTC().Format(time.RFC3339))})
}
func publishINMAPS(db, vault, id, action string, r MemoryReview, apply bool) (MemoryReview, error) {
	if e := validateINMAPS(vault, r.Proposal); e != nil {
		return r, e
	}
	unlock := func() {}
	var e error
	if apply {
		unlock, e = lockFile(filepath.Join(vault, "agent/.candidate-memory.lock"))
		if e != nil {
			return r, e
		}
	}
	defer unlock()
	state := "promoted"
	if action == "reject" {
		state = "rejected"
	}
	if action == "accept" && r.State == "rejected" {
		return r, fmt.Errorf("revise rejected proposal before acceptance")
	}
	if r.State == state && (action == "reject" || r.PublishedRevision == r.Revision) {
		if r.NotePath == "" {
			return r, nil
		}
		b, e := os.ReadFile(filepath.Join(vault, r.NotePath))
		if e == nil && digest(string(b)) == r.FileHash {
			return r, nil
		}
		return r, fmt.Errorf("published note externally changed")
	}
	draft := draftPath(vault, id, r.Revision)
	draftBytes, e := os.ReadFile(draft)
	if e != nil {
		return r, fmt.Errorf("stage the reviewed proposal in Inbox first: %w", e)
	}
	if field(string(draftBytes), "memory_revision") != r.Revision {
		return r, fmt.Errorf("draft revision mismatch")
	}
	at := time.Now().UTC().Format(time.RFC3339)
	changes := map[string][]byte{}
	oldPath := r.NotePath
	if oldPath == "" && r.Proposal.ExistingFactHash != "" {
		paths, _ := filepath.Glob(filepath.Join(vault, "01 - Notes", "*.md"))
		for _, p := range paths {
			b, e := os.ReadFile(p)
			if e != nil {
				return r, e
			}
			if digest(string(b)) == r.Proposal.ExistingFactHash {
				if oldPath != "" {
					return r, fmt.Errorf("ambiguous adoption hash")
				}
				oldPath, _ = filepath.Rel(vault, p)
				r.FileHash = r.Proposal.ExistingFactHash
			}
		}
		if oldPath == "" {
			return r, fmt.Errorf("inspected adoption hash not found")
		}
	}
	oldRaw := ""
	if oldPath != "" {
		b, e := os.ReadFile(filepath.Join(vault, oldPath))
		if e != nil {
			return r, e
		}
		oldRaw = string(b)
		if r.Proposal.ExistingFactHash != "" && digest(oldRaw) == r.Proposal.ExistingFactHash {
			r.FileHash = r.Proposal.ExistingFactHash
		}
		if digest(oldRaw) != r.FileHash && !(r.PendingNotePath != "" && field(oldRaw, "superseded_by") == r.PendingNotePath && field(oldRaw, "memory_revision") == r.PublishedRevision) {
			return r, fmt.Errorf("canonical note externally edited")
		}
	}
	nextPath := oldPath
	if action == "accept" {
		// Reserve identity in the review before any files: a crash retries the same ID.
		nextPath = r.PendingNotePath
		if nextPath == "" {
			day := time.Now().UTC().Format("20060102")
			for n := 1; n <= 9999; n++ {
				p := filepath.Join("01 - Notes", fmt.Sprintf("%s%04d.md", day, n))
				if _, e := os.Lstat(filepath.Join(vault, p)); os.IsNotExist(e) {
					nextPath = p
					break
				}
			}
			if nextPath == "" {
				return r, fmt.Errorf("daily ID space exhausted")
			}
			r.NotePath = oldPath
			r.PendingNotePath = nextPath
			if apply {
				if e = saveMemory(db, id, r); e != nil {
					return r, e
				}
			}
		}
		data := noteBytes(id, strings.TrimSuffix(filepath.Base(nextPath), ".md"), "stable", r, at)
		if b, e := os.ReadFile(filepath.Join(vault, nextPath)); e == nil {
			if field(string(b), "memory_revision") != r.Revision {
				return r, fmt.Errorf("reserved note collision")
			}
			data = b
		}
		changes[filepath.Join(vault, nextPath)] = data
	}
	if oldPath != "" {
		v := replaceField(oldRaw, "status", "deprecated")
		v = replaceField(v, "memory_status", "rejected")
		v = replaceField(v, "updated", at)
		if action == "accept" {
			v = replaceField(v, "superseded_by", scalar(nextPath))
		}
		changes[filepath.Join(vault, oldPath)] = []byte(v)
	}
	draftText := replaceField(string(draftBytes), "status", "deprecated")
	draftText = replaceField(draftText, "updated", at)
	if nextPath != "" {
		draftText = replaceField(draftText, "published_note", scalar(nextPath))
	}
	changes[draft] = []byte(draftText)
	indexPath := filepath.Join(vault, "agent/index.md")
	index, e := os.ReadFile(indexPath)
	if e != nil {
		return r, e
	}
	var lines []string
	var oldAliases []string
	json.Unmarshal([]byte(field(oldRaw, "aliases")), &oldAliases)
	for _, l := range strings.Split(strings.TrimRight(string(index), "\n"), "\n") {
		aliasLink := false
		for _, a := range oldAliases {
			if strings.Contains(l, "[["+a+"]]") {
				aliasLink = true
			}
		}
		if aliasLink {
			continue
		}
		if oldPath != "" && (strings.Contains(l, strings.ReplaceAll(oldPath, " ", "%20")) || strings.Contains(l, oldPath)) {
			continue
		}
		if nextPath != "" && strings.Contains(l, strings.ReplaceAll(nextPath, " ", "%20")) {
			continue
		}
		lines = append(lines, l)
	}
	if action == "accept" {
		lines = append(lines, "- ["+r.Proposal.Title+"](../"+strings.ReplaceAll(nextPath, " ", "%20")+") — "+r.Proposal.Description)
	}
	text := strings.Join(lines, "\n") + "\n"
	count := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "- ") {
			count++
		}
	}
	if count > 160 || len(lines) > 200 || len(text) > 25*1024 {
		return r, fmt.Errorf("index cap exceeded")
	}
	changes[indexPath] = []byte(text)
	if !apply {
		r.WouldState = state
		return r, nil
	}
	if e = writeSet(vault, changes); e != nil {
		return r, e
	}
	r.State = state
	r.Vault = vault
	r.Changed = true
	r.NotePath = nextPath
	r.PendingNotePath = ""
	if action == "accept" {
		r.PublishedRevision = r.Revision
	}
	if nextPath != "" {
		r.FileHash = digest(string(changes[filepath.Join(vault, nextPath)]))
	}
	e = saveMemory(db, id, r)
	return r, e
}

// MarkSourceDrift invalidates dependent stable notes without rewriting meaning.
// Re-acknowledging the source alone never re-accepts a dependent note.
func MarkSourceDrift(vault, sourceID string) (int, error) {
	unlock, e := lockFile(filepath.Join(vault, "agent/.candidate-memory.lock"))
	if e != nil {
		return 0, e
	}
	defer unlock()
	paths, _ := filepath.Glob(filepath.Join(vault, "01 - Notes", "*.md"))
	changes := map[string][]byte{}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			return 0, e
		}
		raw := string(b)
		if field(raw, "status") != "stable" || !strings.Contains(field(raw, "source"), "catalogue_source="+sourceID+";") {
			continue
		}
		if field(raw, "review_state") == "needs_review" {
			continue
		}
		raw = replaceField(raw, "review_state", "needs_review")
		raw = replaceField(raw, "updated", time.Now().UTC().Format(time.RFC3339))
		changes[p] = []byte(raw)
	}
	if len(changes) == 0 {
		return 0, nil
	}
	return len(changes), writeSet(vault, changes)
}

// WriteRosterPointer maintains the single INMAPS agents routing note. The roster
// itself remains in its source repository; no definitions are copied into memory.
func WriteRosterPointer(vault, roster string) error {
	st, e := os.Stat(roster)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("roster must be a regular file")
	}
	unlock, e := lockFile(filepath.Join(vault, "agent/.candidate-memory.lock"))
	if e != nil {
		return e
	}
	defer unlock()
	at := time.Now().UTC().Format(time.RFC3339)
	// tags carries "source" -- a flat, lowercase, governed tag (99 - Meta/
	// tags.md) -- never a namespaced "kind/doc" value: this note has no
	// separate kind: frontmatter field to make that redundant, but
	// inmaps-spec section 3 still forbids a kind value in tags at all.
	text := fmt.Sprintf("---\ntype: Source\nid: agents-roster-routing\ntitle: Agent roster\ndescription: Route to canonical agent definitions and assigned seats.\ntags: [%q]\ncreated: %s\nupdated: %s\nstatus: stable\nsource: %s\n---\n\n# Agent roster\n\n[Open the agent-dotfiles roster](%s).\n\nThis is one routing pointer, not a memory store. The roster distinguishes\ndefinitions from assigned seats and does not claim process liveness. The\nlinked branch is pending review; retarget to the canonical checkout after\nintegration. Per-agent memory format remains reserved to Jon.\n", "source", at, at, scalar(roster), strings.ReplaceAll(roster, " ", "%20"))
	p := Proposal{Type: "Source", Title: "Agent roster", Description: "Canonical roster routing", Learning: "Pointer", Tags: []string{"source"}}
	if e = validateINMAPS(vault, p); e != nil {
		return e
	}
	return writeSet(vault, map[string][]byte{filepath.Join(vault, "03 - Agents/index.md"): []byte(text)})
}
