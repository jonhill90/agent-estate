package candidates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Proposal contains reviewed paraphrases, never an automatically copied prompt.
// AssistantContext is explanatory evidence, never operator authority.
type Proposal struct {
	Slug             string `json:"slug"`
	Type             string `json:"type"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	Learning         string `json:"learning"`
	OperatorContext  string `json:"operator_context"`
	AssistantContext string `json:"assistant_context"`
	Reviewer         string `json:"reviewer"`
	Supersedes       string `json:"supersedes"`
	ExistingFactHash string `json:"existing_fact_hash,omitempty"` // explicit adoption of an inspected, unmanaged fact
}

type MemoryReview struct {
	Proposal          Proposal `json:"proposal"`
	State             string   `json:"state"` // proposed is not promoted
	Revision          string   `json:"revision"`
	PublishedRevision string   `json:"published_revision,omitempty"`
	Vault             string   `json:"vault,omitempty"`
	Citation          string   `json:"citation"`
	Changed           bool     `json:"changed"`
	FileHash          string   `json:"file_hash,omitempty"`
	WouldState        string   `json:"would_state,omitempty"`
}

func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// Advisory locks serialize this workflow's writers. No persistent lock ownership
// is inferred from a stale file; the OS releases the lock when the process exits.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("memory writer busy: %w", err)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func ReadMemory(db, id string) (MemoryReview, error) {
	if _, err := Get(db, id); err != nil {
		return MemoryReview{}, err
	}
	has, err := columnExists(db, "knowledge_candidates", "memory_review")
	if err != nil {
		return MemoryReview{}, err
	}
	if !has {
		return MemoryReview{}, nil
	}
	raw, err := runReadOnly(db, "select memory_review from knowledge_candidates where id='"+sqlEscape(id)+"';")
	if err != nil {
		return MemoryReview{}, err
	}
	var r MemoryReview
	if strings.TrimSpace(raw) != "" {
		err = json.Unmarshal([]byte(raw), &r)
	}
	r.Changed = false
	r.WouldState = ""
	return r, err
}

func saveMemory(db, id string, r MemoryReview) error {
	has, err := columnExists(db, "knowledge_candidates", "memory_review")
	if err != nil {
		return err
	}
	if !has {
		if err = runWrite(db, "ALTER TABLE knowledge_candidates ADD COLUMN memory_review TEXT NOT NULL DEFAULT '';", "add memory review"); err != nil {
			return err
		}
	}
	r.Changed = false
	b, _ := json.Marshal(r)
	return runWrite(db, "UPDATE knowledge_candidates SET memory_review='"+sqlEscape(string(b))+"' WHERE id='"+sqlEscape(id)+"';", "save memory review")
}

func Propose(db, id string, p Proposal, apply bool) (MemoryReview, error) {
	if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(p.Slug) {
		return MemoryReview{}, fmt.Errorf("slug must be a semantic kebab slug")
	}
	if p.ExistingFactHash != "" && !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(p.ExistingFactHash) {
		return MemoryReview{}, fmt.Errorf("existing_fact_hash must be the inspected fact's SHA-256")
	}
	if p.Type != "user" && p.Type != "feedback" && p.Type != "project" && p.Type != "reference" {
		return MemoryReview{}, fmt.Errorf("invalid fact type")
	}
	for _, s := range []string{p.Title, p.Description, p.Reviewer} {
		if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\r\n[]") {
			return MemoryReview{}, fmt.Errorf("title, description and reviewer must be nonempty single lines without brackets")
		}
	}
	if strings.TrimSpace(p.Learning) == "" || strings.TrimSpace(p.OperatorContext) == "" || strings.TrimSpace(p.AssistantContext) == "" {
		return MemoryReview{}, fmt.Errorf("learning and attributed operator/assistant context required")
	}
	unlock := func() {}
	var err error
	if apply {
		unlock, err = lockFile(db + ".memory.lock")
		if err != nil {
			return MemoryReview{}, err
		}
	}
	defer unlock()
	d, err := Get(db, id)
	if err != nil {
		return MemoryReview{}, err
	}
	if d.PromptGone || d.ProvenanceGone || d.ContentHash == "" {
		return MemoryReview{}, fmt.Errorf("cannot propose without resolvable prompt and provenance")
	}
	old, err := ReadMemory(db, id)
	if err != nil {
		return old, err
	}
	if old.Proposal.Slug != "" && old.Proposal.Slug != p.Slug {
		return old, fmt.Errorf("candidate identity is already bound to slug %s", old.Proposal.Slug)
	}
	citation := fmt.Sprintf("candidate=%s; prompt=%s; provenance=%s; harness=%s; session=%s; file=%s; record=%d; content_hash=%s", id, d.PromptID, d.ProvenanceID, d.Harness, d.SessionID, d.SourceFile, d.RecordIndex, d.ContentHash)
	data, _ := json.Marshal(p)
	rev := digest(string(data) + citation)
	if rev == old.Revision {
		return old, nil
	}
	if p.Supersedes != old.PublishedRevision {
		return old, fmt.Errorf("supersedes must equal current published revision %q", old.PublishedRevision)
	}
	r := MemoryReview{Proposal: p, State: "proposed", Revision: rev, PublishedRevision: old.PublishedRevision, Vault: old.Vault, Citation: citation, Changed: apply, FileHash: old.FileHash}
	if apply {
		err = saveMemory(db, id, r)
	}
	return r, err
}

// Publish updates the canonical fact before its index/decision labels. It never
// invents a vault. Backups contain previous bytes, outside facts/ and retrieval.
func Publish(db, vault, id, action string, apply bool) (MemoryReview, error) {
	if action != "accept" && action != "reject" {
		return MemoryReview{}, fmt.Errorf("action must be accept or reject")
	}
	unlock := func() {}
	var err error
	if apply {
		unlock, err = lockFile(db + ".memory.lock")
		if err != nil {
			return MemoryReview{}, err
		}
	}
	defer unlock()
	r, err := ReadMemory(db, id)
	if err != nil {
		return r, err
	}
	if r.Revision == "" {
		return r, fmt.Errorf("propose a cited learning first")
	}
	if action == "accept" {
		d, e := Get(db, id)
		if e != nil {
			return r, e
		}
		citation := fmt.Sprintf("candidate=%s; prompt=%s; provenance=%s; harness=%s; session=%s; file=%s; record=%d; content_hash=%s", id, d.PromptID, d.ProvenanceID, d.Harness, d.SessionID, d.SourceFile, d.RecordIndex, d.ContentHash)
		if d.PromptGone || d.ProvenanceGone || citation != r.Citation {
			return r, fmt.Errorf("source evidence changed; review a new proposal")
		}
	}
	if vault == "" {
		return r, fmt.Errorf("AGENT_MEMORY_VAULT is unset")
	}
	vault, err = filepath.EvalSymlinks(vault)
	if err != nil {
		return r, err
	}
	vault, err = filepath.Abs(vault)
	if err != nil {
		return r, err
	}
	if r.Vault != "" && r.Vault != vault {
		return r, fmt.Errorf("candidate already bound to another vault")
	}
	agent := filepath.Join(vault, "agent")
	for _, dir := range []string{agent, filepath.Join(agent, "facts")} {
		st, e := os.Lstat(dir)
		if e != nil {
			return r, e
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return r, fmt.Errorf("not a plain directory: %s", dir)
		}
	}
	if apply {
		release, e := lockFile(filepath.Join(agent, ".candidate-memory.lock"))
		if e != nil {
			return r, e
		}
		defer release()
	}
	fact := filepath.Join(agent, "facts", r.Proposal.Slug+".md")
	paths := []string{fact, filepath.Join(agent, "index.md"), filepath.Join(agent, "log.md")}
	old := make([][]byte, len(paths))
	for i, p := range paths {
		st, e := os.Lstat(p)
		if os.IsNotExist(e) && i != 1 {
			continue
		}
		if e != nil {
			return r, e
		}
		if !st.Mode().IsRegular() {
			return r, fmt.Errorf("not a regular file: %s", p)
		}
		old[i], e = os.ReadFile(p)
		if e != nil {
			return r, e
		}
	}
	adopting := action == "accept" && r.FileHash == "" && r.PublishedRevision == "" && !strings.Contains(string(old[0]), "\ncandidate_id:") && pHashMatches(r.Proposal.ExistingFactHash, old[0])
	if old[0] != nil && !strings.Contains(string(old[0]), "\ncandidate_id: "+id+"\n") && !adopting {
		return r, fmt.Errorf("slug collision: inspect existing fact %s", fact)
	}
	state := "promoted"
	if action == "reject" {
		state = "rejected"
	}
	if state == "promoted" && r.State == "rejected" {
		return r, fmt.Errorf("rejected proposal requires a revised proposal before acceptance")
	}
	at := time.Now().UTC().Format(time.RFC3339)
	updated := at
	if strings.Contains(string(old[0]), "\nmemory_revision: "+r.Revision+"\n") && strings.Contains(string(old[0]), "\nmemory_status: "+state+"\n") {
		for _, line := range strings.Split(string(old[0]), "\n") {
			if strings.HasPrefix(line, "updated: ") {
				updated = strings.TrimPrefix(line, "updated: ")
				break
			}
		}
	}
	created := at
	for _, line := range strings.Split(string(old[0]), "\n") {
		if strings.HasPrefix(line, "created: ") {
			created = strings.TrimPrefix(line, "created: ")
			break
		}
	}
	p := r.Proposal
	supersedes := p.Supersedes
	if supersedes == "" {
		supersedes = p.ExistingFactHash
	}
	body := p.Learning + "\n\nOperator context (reviewed paraphrase): " + p.OperatorContext + "\n\nAssistant context (not operator instruction): " + p.AssistantContext + "\n"
	factBytes := []byte(fmt.Sprintf("---\ntype: %s\ntitle: %s\ndescription: %s\ncreated: %s\nupdated: %s\nsource: %s\ncandidate_id: %s\nmemory_status: %s\nmemory_revision: %s\nsupersedes: %s\nreviewer: %s\n---\n\n%s", p.Type, p.Title, p.Description, created, updated, r.Citation, id, state, r.Revision, supersedes, p.Reviewer, body))
	if action == "reject" {
		factBytes = nil
	}
	if r.FileHash != "" && digest(string(old[0])) != r.FileHash && string(old[0]) != string(factBytes) {
		return r, fmt.Errorf("canonical fact was externally edited or removed; inspect before overwriting")
	}
	if r.FileHash == "" && old[0] != nil && string(old[0]) != string(factBytes) && !adopting {
		return r, fmt.Errorf("existing fact differs from pending publication")
	}
	var lines []string
	target := "facts/" + p.Slug + ".md"
	linked := false
	entry := "- [" + p.Title + "](" + target + ") — " + p.Description
	for _, line := range strings.Split(strings.TrimRight(string(old[1]), "\n"), "\n") {
		if strings.Contains(line, "]("+target+")") || strings.Contains(line, "[["+p.Slug+"]]") {
			if state == "promoted" && !linked {
				lines = append(lines, entry)
				linked = true
			}
			continue
		}
		lines = append(lines, line)
	}
	if state == "promoted" && !linked {
		lines = append(lines, entry)
	}
	index := strings.Join(lines, "\n") + "\n"
	entries := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "- ") {
			entries++
		}
	}
	if entries > 160 || len(lines) > 200 || len(index) > 25*1024 {
		return r, fmt.Errorf("index cap exceeded; curate existing entries first")
	}
	event := "candidate=" + id + " revision=" + r.Revision + " state=" + state
	log := string(old[2])
	if !strings.Contains(log, event) {
		verb := "Create"
		if old[0] != nil {
			verb = "Update"
		}
		if action == "reject" {
			verb = "Delete"
		}
		log = "## " + at[:10] + "\n\n**" + verb + "** [" + p.Title + "](" + target + ") — " + event + " (" + at[11:] + ")\n\n" + log
	}
	next := [][]byte{factBytes, []byte(index), []byte(log)}
	changed := r.State != state || r.Vault != vault
	for i := range paths {
		if string(next[i]) != string(old[i]) {
			changed = true
		}
	}
	if !apply {
		r.WouldState = state
		return r, nil
	}
	r.State = state
	r.Vault = vault
	r.Changed = apply && changed
	r.FileHash = digest(string(factBytes))
	if action == "reject" {
		r.FileHash = ""
	}
	if state == "promoted" {
		r.PublishedRevision = r.Revision
	}
	if !changed {
		return r, nil
	}
	// Complete all backups before any canonical replacement. Retry repairs partial
	// writes after a crash; a returned error never claims publication succeeded.
	backup, err := os.MkdirTemp(agent, ".memory-backup-")
	if err != nil {
		return r, err
	}
	for i, b := range old {
		if b != nil {
			if err = os.WriteFile(filepath.Join(backup, fmt.Sprintf("%d-%s", i, filepath.Base(paths[i]))), b, 0600); err != nil {
				return r, err
			}
			check, e := os.ReadFile(filepath.Join(backup, fmt.Sprintf("%d-%s", i, filepath.Base(paths[i]))))
			if e != nil || string(check) != string(b) {
				return r, fmt.Errorf("backup verification failed: %s", backup)
			}
		}
	}
	for i, path := range paths {
		if string(next[i]) == string(old[i]) {
			continue
		}
		if i == 0 && next[i] == nil {
			err = os.Remove(path)
		} else {
			err = atomicMemoryWrite(path, next[i])
		}
		if err != nil {
			return r, fmt.Errorf("publication incomplete; backup=%s; retry: %w", backup, err)
		}
	}
	if err = saveMemory(db, id, r); err != nil {
		return r, fmt.Errorf("fact written, decision incomplete; retry: %w", err)
	}
	return r, nil
}

func pHashMatches(expected string, data []byte) bool {
	return expected != "" && data != nil && digest(string(data)) == expected
}

func atomicMemoryWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".memory-write-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
