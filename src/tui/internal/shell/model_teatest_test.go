package shell

// This file's tests DRIVE the real tea.Program via
// charmbracelet/x/exp/teatest -- sending tea.KeyMsg through the same event
// loop the compiled binary uses, then reading the program's actual
// rendered output -- the same discipline internal/board's own
// model_teatest_test.go already uses. Agent-tui#38's own acceptance is
// explicit: "Demonstrated by driving the navigation keys, not by a
// screenshot," and "a control that is not pressed is not proven" is the
// same agent-tui#23 lesson this repo has already paid for once ([a]ttach shipped
// dead because nothing ever pressed it).

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/jonhill90/agent-estate/src/tui/internal/admin"
	"github.com/jonhill90/agent-estate/src/tui/internal/agents"
	"github.com/jonhill90/agent-estate/src/tui/internal/apidocs"
	"github.com/jonhill90/agent-estate/src/tui/internal/board"
	"github.com/jonhill90/agent-estate/src/tui/internal/chat"
	"github.com/jonhill90/agent-estate/src/tui/internal/connectors"
	"github.com/jonhill90/agent-estate/src/tui/internal/cost"
	"github.com/jonhill90/agent-estate/src/tui/internal/dashboard"
	"github.com/jonhill90/agent-estate/src/tui/internal/external"
	"github.com/jonhill90/agent-estate/src/tui/internal/flow"
	"github.com/jonhill90/agent-estate/src/tui/internal/gallery"
	"github.com/jonhill90/agent-estate/src/tui/internal/knowledge"
	"github.com/jonhill90/agent-estate/src/tui/internal/lane"
	"github.com/jonhill90/agent-estate/src/tui/internal/library"
	"github.com/jonhill90/agent-estate/src/tui/internal/mcpservers"
	"github.com/jonhill90/agent-estate/src/tui/internal/monitor"
	"github.com/jonhill90/agent-estate/src/tui/internal/rail"
	"github.com/jonhill90/agent-estate/src/tui/internal/secrets"
	"github.com/jonhill90/agent-estate/src/tui/internal/skills"
	"github.com/jonhill90/agent-estate/src/tui/internal/workflows"
)

// testModel builds a Model wired to fakes only -- no MCP, no ccusage, no
// gh, no ledger -- the same "construct from fakes, never a subprocess"
// discipline every other package's tests in this repo already follow (see
// AGENTS.md's Adapter discipline section). boardOK true with one card is
// what lets TestLeaderTNavigatesToBoardPane assert on real board content rather
// than only the unavailable placeholder.
func testModel() Model {
	r := rail.New(func() ([]lane.Lane, error) { return nil, nil })
	snap := board.Snapshot{
		Cards: []board.Card{{Repo: board.Repo{Owner: "o", Name: "r"}, Number: 7, Column: board.Backlog, Title: "seven"}},
		Repos: []board.Repo{{Owner: "o", Name: "r"}},
	}
	b := board.New(func() (board.Snapshot, error) { return snap, nil })
	c := cost.New(func() (cost.Snapshot, error) { return cost.Snapshot{}, nil })
	g := gallery.New()
	fl := flow.New()
	ch := chat.New(chat.NewFixtureSource())

	ag := agents.New(func() ([]lane.Session, error) {
		return []lane.Session{{Name: "director", Lanes: []lane.Lane{{Name: "w1", State: "busy"}}}}, nil
	})
	sk := skills.New(func() ([]skills.Skill, error) {
		return []skills.Skill{{Dir: "test-marker-skill", Name: "test-marker-skill", Description: "a fake skill for shell-level routing tests"}}, nil
	})
	mc := mcpservers.New(func() ([]mcpservers.Server, error) {
		return []mcpservers.Server{{Name: "test-marker-server", Scope: mcpservers.ScopeGlobal, Transport: mcpservers.TransportStdio, Command: "python3"}}, nil
	})
	co := connectors.New(func() ([]connectors.Connection, []connectors.AvailableModel, error) {
		return []connectors.Connection{{Harness: connectors.HarnessCodex, Provider: "openai", Configured: true}}, nil, nil
	})
	ad := admin.New(func() (admin.Snapshot, error) {
		return admin.Snapshot{Services: []admin.Service{{Name: "test-marker-container", Image: "x", Status: "Up"}}}, nil
	})
	da := dashboard.New(func() (dashboard.Stats, error) {
		return dashboard.Stats{AgentsKnown: true, AgentsByState: map[string]int{"busy": 1}}, nil
	})
	kn := knowledge.New(
		func() ([]knowledge.IndexEntry, error) {
			return []knowledge.IndexEntry{{Slug: "test-marker-fact", Title: "test marker fact", Description: "a fake vault fact for shell-level routing tests"}}, nil
		},
		func(slug string) (knowledge.Fact, error) {
			return knowledge.Fact{Slug: slug, Title: "test marker fact", Body: "fake body"}, nil
		},
	)
	lb := library.New(
		func(view library.View, weight, status string) ([]library.ItemRow, error) {
			return []library.ItemRow{{ID: "it-deadbeef", Kind: "fact", Weight: "hard", Status: "open", BodySnippet: "test-marker-item"}}, nil
		},
		func(id string) (library.ItemDetail, error) {
			return library.ItemDetail{ID: id, Kind: "fact"}, nil
		},
		func() (int, error) { return 1, nil },
	)
	mo := monitor.New(func() (monitor.Snapshot, error) {
		return monitor.Snapshot{
			Host:   monitor.Host{Cores: 4, ClaudeProcesses: monitor.KnownCount(2)},
			Agents: monitor.AgentHealth{Known: true, ByState: map[string]int{"busy": 1}, Total: 1},
		}, nil
	})
	wf := workflows.New(func() ([]board.TaskRow, error) {
		return []board.TaskRow{{TaskID: "test-marker-task", Lane: "test-marker-lane", TaskStatus: "complete", CreatedAt: 1}}, nil
	})
	ap := apidocs.New(func() (apidocs.Reference, error) {
		return apidocs.Reference{
			Title:      "Test API",
			Version:    "0.0.1",
			SourcePath: "/fake/openapi.yaml",
			PathCount:  1,
			Endpoints:  []apidocs.Endpoint{{Method: "GET", Path: "/test-marker-endpoint", Summary: "a fake operation for shell-level routing tests"}},
		}, nil
	})
	// The external pane's opener is nil here on purpose: a shell routing
	// test must never be able to launch a browser on the machine running
	// it. Its view still renders the destination, which is what these
	// tests assert on.
	ex := external.New(nil)

	se := secrets.New(func() (secrets.Inventory, error) {
		return secrets.Inventory{
			SourcePath: "/fake/secrets-schema.yaml",
			TotalKeys:  1,
			Paths: []secrets.Path{
				{VaultPath: "secret/test", Keys: []secrets.Key{{Name: "test-marker-key", Consumers: []string{"api"}}}},
			},
		}, nil
	})

	return New(r, b, true, "", c, g, fl, ch).
		WithAgents(ag).
		WithSkills(sk).
		WithMCPServers(mc).
		WithConnectors(co).
		WithAdmin(ad).
		WithDashboard(da).
		WithKnowledge(kn).
		WithLibrary(lb).
		WithMonitor(mo).
		WithWorkflows(wf).
		WithAPIDocs(ap).
		WithExternal(ex).
		WithSecrets(se)
}

func run(t *testing.T, m Model) *teatest.TestModel {
	t.Helper()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
	t.Cleanup(func() { _ = tm.Quit() })
	return tm
}

// TestNoFlagOpensHomePaneWithRailVisible drives a plain New(...) (no
// WithStart) start and asserts the home pane's own nav legend renders --
// agent-tui#38 acceptance item 1, "no-flag launch opens the app."
func TestNoFlagOpensHomePaneWithRailVisible(t *testing.T) {
	tm := run(t, testModel())
	out := waitFor(t, tm, "[space] menu")
	if !bytes.Contains(out, []byte("[tab] focus:rail")) {
		t.Fatalf("footer missing on initial frame:\n%s", out)
	}
}

// TestLeaderTNavigatesToBoardPane presses f2 against a real running Program and
// asserts the board pane's own content actually replaces the home pane's
// -- not that the key was accepted, that the VIEW changed to prove it.
func TestLeaderTNavigatesToBoardPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "[space] menu")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	waitFor(t, tm, "#7 ")
}

// TestLeaderUNavigatesToCostPane is TestLeaderTNavigatesToBoardPane's sibling for
// the cost pane -- cost.Model.View's own titleStyle line ("cost") is the
// marker, same as board's card-number marker above.
func TestLeaderUNavigatesToCostPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "[space] menu")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	waitFor(t, tm, "cost")
}

// TestLeaderGNavigatesToGalleryPane is the same drive for the gallery pane.
func TestLeaderGNavigatesToGalleryPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "[space] menu")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	waitFor(t, tm, "glyph gallery")
}

// TestLeaderFNavigatesToFlowPane is TestLeaderTNavigatesToBoardPane's sibling for the
// flow pane (agent-tui#64) -- flow's own title line is the marker.
func TestLeaderFNavigatesToFlowPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "[space] menu")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	waitFor(t, tm, "flow -- work moving")
}

// TestFlowUnavailableRendersInPlaceOfFlow mirrors
// TestBoardUnavailableRendersInPlaceOfBoard: flow reads the exact same
// board.Fetcher, so boardOK == false must gate it the same way, never an
// unfetchable flow.Model stuck on a permanent fetch error.
func TestFlowUnavailableRendersInPlaceOfFlow(t *testing.T) {
	r := rail.New(func() ([]lane.Lane, error) { return nil, nil })
	b := board.New(func() (board.Snapshot, error) { return board.Snapshot{}, nil })
	c := cost.New(func() (cost.Snapshot, error) { return cost.Snapshot{}, nil })
	g := gallery.New()
	fl := flow.New()
	ch := chat.New(chat.NewFixtureSource())
	m := New(r, b, false, "no -ledger configured", c, g, fl, ch)

	tm := run(t, m)
	waitFor(t, tm, "[space] menu")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	waitFor(t, tm, "board unavailable")
}

// TestLeaderCNavigatesToChatPane is the same drive for the chat pane
// (agent-tui#20) -- chat.Model.View's own title line ("chat") plus its
// fixture thread title is the marker, same shape as board/cost/gallery's
// own navigation tests above. Chat is [6], not [5]: agent-tui#68 (flow,
// agent-tui#64) landed on main first and already claimed [5] -- see
// this rebase's own commit message for the conflict this resolves.
func TestLeaderCNavigatesToChatPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "[space] menu")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	waitFor(t, tm, "lane/20-chat-threads")
}

// TestF1ReturnsToHomePane closes the last shell-footer gap: f2/f3/f4 were
// each driven to prove they navigate INTO a pane, but nothing previously
// pressed f1 to prove the way back to home works too.
func TestF1ReturnsToHomePane(t *testing.T) {
	// homeMarker is homeView's own line, not the footer's -- the footer's
	// "[space] menu" is present on EVERY pane, so it cannot tell "we are on
	// home" apart from "we are on gallery with the footer still showing".
	const homeMarker = "[tab] move focus into the sidebar"

	tm := run(t, testModel())
	waitFor(t, tm, homeMarker)

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	waitFor(t, tm, "glyph gallery")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	waitFor(t, tm, homeMarker)
}

// TestTabTogglesFocusBetweenRailAndContent drives [tab] and asserts the
// shell's own footer legend -- the one place focus is rendered -- actually
// flips from "focus:rail" to "focus:content" and back. This is the seam
// routeKey depends on: a broken toggleFocus would leave this byte-for-byte
// identical, exactly the failure mode agent-tui#29/agent-tui#23 both warn about.
func TestTabTogglesFocusBetweenRailAndContent(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "focus:rail")

	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	waitFor(t, tm, "focus:content")

	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	waitFor(t, tm, "focus:rail")
}

// TestCtrlCQuitsFromAnyPane drives ctrl+c after navigating away from home,
// asserting the real Program actually terminates -- agent-tui#38's
// acceptance item "q/ctrl+c quits from every pane," and the agent-tui#22 trap this
// repo has already shipped once (a mode that swallowed every key,
// including quit).
func TestCtrlCQuitsFromAnyPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "[space] menu")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	waitFor(t, tm, "glyph gallery")

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}

// TestQQuitsFromHomePane drives plain "q" from the home pane (which has no
// live sub-model of its own -- see Model.homeKey) and asserts the Program
// terminates, exactly like ctrl+c does.
func TestQQuitsFromHomePane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "[space] menu")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}

// TestBoardUnavailableRendersInPlaceOfBoard is the boardOK==false path --
// reaching the board pane by navigation with no -ledger configured must
// render the shell's own notice, never an unfetchable board.Model stuck on
// a permanent fetch error.
func TestBoardUnavailableRendersInPlaceOfBoard(t *testing.T) {
	r := rail.New(func() ([]lane.Lane, error) { return nil, nil })
	b := board.New(func() (board.Snapshot, error) { return board.Snapshot{}, nil })
	c := cost.New(func() (cost.Snapshot, error) { return cost.Snapshot{}, nil })
	g := gallery.New()
	fl := flow.New()
	ch := chat.New(chat.NewFixtureSource())
	m := New(r, b, false, "no -ledger configured", c, g, fl, ch)

	tm := run(t, m)
	waitFor(t, tm, "[space] menu")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	waitFor(t, tm, "board unavailable")
}

// TestWithStartOpensOnTheChosenPane is the -board/-cost/-gallery flags'
// own contract: cmd/agent-tui's remaining job for them is choosing where
// the app OPENS, not launching a second program.
func TestWithStartOpensOnTheChosenPane(t *testing.T) {
	m := testModel().WithStart(PaneGallery)
	tm := run(t, m)
	waitFor(t, tm, "glyph gallery")
}

// waitFor reads tm's output until it contains want, returning everything
// accumulated so far -- teatest.WaitFor exists but only reports pass/fail
// and discards the bytes; board's own drainUntil (model_teatest_test.go)
// takes the same approach for the same reason: a failure message needs the
// actual rendered frame, not just "condition never true."
func waitFor(t *testing.T, tm *teatest.TestModel, want string) []byte {
	t.Helper()
	var b bytes.Buffer
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		buf := make([]byte, 65536)
		n, _ := tm.Output().Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if bytes.Contains(b.Bytes(), []byte(want)) {
			return b.Bytes()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor %q: not seen after 8s. Output so far:\n%s", want, b.String())
	return nil
}

// TestFinderJumpsThroughARealProgram closes the gap a council seat named:
// every leader chord was driven end-to-end here, but the finder never was.
// "A control that is not pressed is not proven" applies to it too -- and the
// finder is the one control that can send you somewhere you did not choose.
func TestFinderJumpsThroughARealProgram(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "[space] menu")

	// <space><space> opens the jump list.
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	waitFor(t, tm, "find ›")

	// Fuzzy: "knw" must find Knowledge without those letters being adjacent.
	for _, r := range "knw" {
		tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	waitFor(t, tm, "Knowledge")

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	// The Knowledge pane's own content -- its fact table header -- not the
	// finder's echo of the route name, which would prove only that the text
	// was typed.
	waitFor(t, tm, "SLUG")
}

// Escape must close the finder without navigating. A jump list that moves
// you on the way out is worse than one that does nothing.
//
// Asserted at the Model level rather than through teatest: the program only
// redraws changed regions, so "the finder is gone" is not reliably visible in
// a terminal diff. The end-to-end wiring is proven by
// TestFinderJumpsThroughARealProgram above; this proves the semantics.
func TestFinderEscapeDoesNotNavigate(t *testing.T) {
	m := testModel()
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	base := sized.(Model)
	before := base.active

	a, _ := base.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	b, _ := a.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	open := b.(Model)
	if !open.finderOpen {
		t.Fatal("<space><space> must open the finder")
	}
	for _, r := range "knw" {
		nx, _ := open.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		open = nx.(Model)
	}
	closed, _ := open.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := closed.(Model)
	if got.finderOpen {
		t.Error("esc must close the finder")
	}
	if got.active != before {
		t.Errorf("esc must not navigate: active moved %v -> %v", before, got.active)
	}
}
