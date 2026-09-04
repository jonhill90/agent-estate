package knowledge

import (
	"errors"
	"testing"
)

func TestStarsSourceParsesEachRepoAsOneItem(t *testing.T) {
	fake := func(args ...string) ([]byte, error) {
		return []byte(
			`{"full_name":"a/one","html_url":"https://github.com/a/one","description":"first","topics":["go","cli"]}` + "\n" +
				`{"full_name":"b/two","html_url":"https://github.com/b/two","description":"","topics":[]}` + "\n",
		), nil
	}
	res, items := starsSource(fake)

	if !res.OK || res.Count != 2 {
		t.Fatalf("starsSource() result = %+v", res)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Permalink != "https://github.com/a/one" {
		t.Errorf("Permalink = %q", items[0].Permalink)
	}
	if len(items[0].SynapticTags) != 2 || items[0].SynapticTags[0] != "#go" {
		t.Errorf("SynapticTags = %v, want #hashtag-prefixed topics", items[0].SynapticTags)
	}
}

// TestStarsSourceUnreadableIsHonestNotEmpty is the "instrument that
// cannot see a thing looks like the thing being absent" case
// (AGENTS.md): gh failing must surface as OK=false with a real reason,
// never as a silently zero-item source indistinguishable from "no
// stars".
func TestStarsSourceUnreadableIsHonestNotEmpty(t *testing.T) {
	fake := func(args ...string) ([]byte, error) {
		return nil, errors.New("gh: not authenticated")
	}
	res, items := starsSource(fake)
	if res.OK {
		t.Fatal("starsSource() reported OK for a failing gh call")
	}
	if res.Reason == "" {
		t.Fatal("starsSource() gave no reason for the failure")
	}
	if items != nil {
		t.Fatalf("starsSource() returned items despite failure: %v", items)
	}
}

func TestStarsSourceIDsDoNotCollide(t *testing.T) {
	fake := func(args ...string) ([]byte, error) {
		return []byte(
			`{"full_name":"a/one","html_url":"x"}` + "\n" +
				`{"full_name":"a/two","html_url":"y"}` + "\n" +
				`{"full_name":"a/three","html_url":"z"}` + "\n",
		), nil
	}
	_, items := starsSource(fake)
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.ID] {
			t.Fatalf("duplicate id %q", it.ID)
		}
		seen[it.ID] = true
	}
}
