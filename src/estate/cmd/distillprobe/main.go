// Command distillprobe reports what distillation would propose over the live
// corpus, without writing anything. It exists to size the result before the
// verb that writes facts is wired up: a grouping producing ten groups or ten
// thousand would both be wrong, and only a run against the real bodies shows
// which. Fixtures cannot answer this question.
//
// Reads through the sqlite3 CLI in the URI form, matching internal/corpus --
// the bare -readonly flag has been seen failing under WAL contention where
// file:...?mode=ro succeeded on the same file seconds later.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
	"github.com/jonhill90/agent-estate/estate/internal/distill"
)

// sep is a separator no item body contains, so a body carrying a pipe or a
// tab cannot split a row into the wrong number of fields.
const sep = "\x1f"

func main() {
	threshold := flag.Float64("threshold", 0.6, "minimum similarity for two items to group")
	min := flag.Int("min", 3, "smallest group worth proposing")
	minScore := flag.Float64("min-score", 0.4, "reject groups below this mean similarity")
	show := flag.Int("show", 12, "how many groups to print in full")
	flag.Parse()

	path, err := corpus.Path()
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus path:", err)
		os.Exit(1)
	}
	q := `select i.id, i.kind, i.status, p.at, replace(i.body, char(10), ' ')
	  from items i join prompts p on p.id = i.prompt_id
	  where i.weight = 'hard' and i.status != 'dropped'
	    and i.kind in ('parameter','directive','correction')`
	out, err := exec.Command("sqlite3", "-separator", sep, "file:"+path+"?mode=ro&immutable=1", q).Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sqlite3:", err)
		os.Exit(1)
	}

	var items []distill.Item
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Split(sc.Text(), sep)
		if len(f) != 5 {
			continue
		}
		at, _ := strconv.ParseInt(f[3], 10, 64)
		items = append(items, distill.Item{ID: f[0], Kind: f[1], Status: f[2], At: at, Body: f[4]})
	}

	groups := distill.Partition(items, distill.Options{Threshold: *threshold, MinMembers: *min, MinScore: *minScore})

	covered := 0
	sizes := map[int]int{}
	for _, g := range groups {
		covered += len(g.Members)
		sizes[len(g.Members)]++
	}
	fmt.Printf("items considered: %d\n", len(items))
	fmt.Printf("groups proposed:  %d\n", len(groups))
	if len(items) > 0 {
		fmt.Printf("items covered:    %d (%.1f%%)\n", covered, 100*float64(covered)/float64(len(items)))
	}
	fmt.Printf("items left alone: %d\n\n", len(items)-covered)

	var ks []int
	for k := range sizes {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	fmt.Println("group size distribution:")
	for _, k := range ks {
		fmt.Printf("  %3d members  x%d\n", k, sizes[k])
	}

	fmt.Printf("\nlargest %d groups:\n", *show)
	for i, g := range groups {
		if i >= *show {
			break
		}
		fmt.Printf("\n[%s] %d members, mean similarity %.2f\n", g.Key, len(g.Members), g.Score)
		fmt.Printf("  CANONICAL (%s/%s): %s\n", g.Canonical.Kind, g.Canonical.ID, trunc(g.Canonical.Body, 160))
		n := 0
		for _, m := range g.Members {
			if m.ID == g.Canonical.ID {
				continue
			}
			if n++; n > 4 {
				fmt.Printf("    ... and %d more\n", len(g.Members)-1-4)
				break
			}
			fmt.Printf("    evidence: %s\n", trunc(m.Body, 110))
		}
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
