package corpus

// Provenance auditing.
//
// A parameter is only law if it traces to something the operator actually
// said. At least one hard parameter was manufactured from a complaint: the
// operator objected to machinery hidden in ~/.local, and the itemiser filed
// "~/.local holds state like logs and status" -- a storage policy he never
// stated -- as his hard law. It was then quoted back to him as his own words.
//
// Every item carries prompt_id, so this is checkable and never has been. This
// finds parameters whose text is not supported by the prompt behind them.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type Finding struct {
	ItemID    string
	Param     string
	Prompt    string
	Overlap   float64  // fraction of the parameter's content words present in the prompt
	Invented  []string // directive words asserted by the parameter but absent from the prompt
	PromptIsQuestion bool
}

// directive words turn a remark into law. If the parameter asserts one and the
// source prompt does not, the obligation was added by the itemiser.
var directives = []string{"never", "always", "must", "only", "holds", "goes in", "belongs in", "required", "forbidden", "do not", "shall"}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(w) > 3 {
			m[w] = true
		}
	}
	return m
}

// Audit returns hard parameters ranked by how little their source prompt
// supports them, worst first.
func Audit() ([]Finding, error) {
	p, err := dbPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("corpus unreadable at %s: %w", p, err)
	}
	q := `select i.id, replace(replace(i.body,char(10),' '),char(13),' '),
	             replace(replace(p.text_raw,char(10),' '),char(13),' ')
	      from items i join prompts p on p.id = i.prompt_id
	      where i.kind='parameter' and i.weight='hard'`
	out, err := exec.Command("sqlite3", "-separator", sep, "file:"+p+"?mode=ro&immutable=1", q).Output()
	if err != nil {
		return nil, fmt.Errorf("provenance query failed: %w", err)
	}
	var fs []Finding
	s := bufio.NewScanner(strings.NewReader(string(out)))
	s.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for s.Scan() {
		parts := strings.SplitN(s.Text(), sep, 3)
		if len(parts) != 3 {
			continue
		}
		id, param, prompt := parts[0], parts[1], parts[2]
		pw, sw := words(param), words(prompt)
		if len(pw) == 0 {
			continue
		}
		hit := 0
		for w := range pw {
			if sw[w] {
				hit++
			}
		}
		f := Finding{
			ItemID:  id,
			Param:   param,
			Prompt:  prompt,
			Overlap: float64(hit) / float64(len(pw)),
			PromptIsQuestion: strings.Contains(prompt, "?"),
		}
		lowParam, lowPrompt := strings.ToLower(param), strings.ToLower(prompt)
		for _, d := range directives {
			if strings.Contains(lowParam, d) && !strings.Contains(lowPrompt, d) {
				f.Invented = append(f.Invented, d)
			}
		}
		fs = append(fs, f)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(fs) == 0 {
		return nil, fmt.Errorf("provenance audit found zero hard parameters -- refusing to report that as clean")
	}
	sort.SliceStable(fs, func(a, b int) bool {
		if len(fs[a].Invented) != len(fs[b].Invented) {
			return len(fs[a].Invented) > len(fs[b].Invented)
		}
		return fs[a].Overlap < fs[b].Overlap
	})
	return fs, nil
}
