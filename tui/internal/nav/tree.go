// Package nav is agent-tui#38's follow-on (docs/SPEC-shell.md, item S1):
// the nav model 1:1 with the hill90 web app's own left nav
// (hill90-app/services/ui/src/components/nav-items.ts). This file is pure
// data plus Flatten() for keyboard traversal -- no rendering, no Bubble
// Tea Model, no key handling. Rendering is model.go/view.go (S2).
//
// ui_fidelity=1:1 (SPEC-shell.md) is read against nav-items.ts as it stands
// today, read as admin -- Jon is hill90-ui:admin,user, so every
// adminOnly entry (Storage, Secrets, API Docs, the whole Admin group) is
// already included below rather than gated behind a visibility flag; there
// is no signed-out or non-admin path this TUI needs to model, so
// NavLink.adminOnly/NavGroup.adminOnly from the source file have no
// equivalent field here -- they were already resolved to "always visible"
// before this tree was written.
package nav

// Kind distinguishes how an Item behaves, not how it looks. Most items are
// KindRoute -- href navigation within hill90 core web pages/agent-tui panes.
// KindExternal marks nav-items.ts's one external: true entry (Platform
// Docs, which points at https://docs.hill90.com -- a browser destination
// no TUI pane can open). KindNative marks the one item nav-items.ts has no
// entry for at all: Lanes, which SPEC-shell.md calls out by name as "the
// one item with no web equivalent, because it is what the web app cannot
// do."
type Kind int

const (
	KindRoute Kind = iota
	KindExternal
	KindNative
)

// Item is one leaf nav destination. Icon is the exact lucide-react
// component name nav-items.ts imports it as (e.g. "Home",
// "LayoutDashboard") -- source-fidelity data, not a rendering choice; see
// view.go's iconGlyphs for how a terminal approximates each one.
type Item struct {
	ID    string
	Label string
	Icon  string
	Kind  Kind

	// URL is nav-items.ts's own `href` for a KindExternal item, and empty
	// for everything else -- a KindRoute's href is a web path this TUI
	// does not navigate to (it routes to a pane instead), so carrying it
	// would be data nothing reads. It exists because internal/shell's
	// external pane has to be able to NAME the destination: the route was
	// declared external here from the day this tree was written while the
	// shell rendered it as an unbuilt stub, and a pane cannot say where a
	// link goes if the tree does not carry it.
	URL string
}

// Group is one collapsible section (nav-items.ts's NavGroup). Children are
// always KindRoute or KindExternal -- no group in nav-items.ts contains a
// native-only child, so Group carries no Kind of its own (SPEC-shell.md's
// own field list: "Group{ID, Label, Children}").
type Group struct {
	ID       string
	Label    string
	Children []Item
}

// Tree is the full nav structure Tree() returns: Items is the top-level
// row (nav-items.ts's ungrouped NavLinks, plus Lanes appended last),
// Groups is every collapsible section below it, in nav-items.ts's own
// order.
type Tree struct {
	Items  []Item
	Groups []Group
}

// Build returns the nav tree, matching
// hill90-app/services/ui/src/components/nav-items.ts's NAV_ITEMS 1:1 (ID,
// Label and Icon lifted from that file directly) with Lanes appended --
// SPEC-shell.md's "Plus Lanes at top level" line. Read from disk
// 2026-08-22; re-check nav-items.ts before trusting this if hill90-app has
// since added or renamed an entry.
func Build() Tree {
	return Tree{
		Items: []Item{
			{ID: "home", Label: "Home", Icon: "Home", Kind: KindRoute},
			{ID: "dashboard", Label: "Dashboard", Icon: "LayoutDashboard", Kind: KindRoute},
			{ID: "agents", Label: "Agents", Icon: "Bot", Kind: KindRoute},
			{ID: "chat", Label: "Chat", Icon: "MessageSquare", Kind: KindRoute},
			{ID: "tasks", Label: "Tasks", Icon: "CheckSquare", Kind: KindRoute},
			{ID: "knowledge", Label: "Knowledge", Icon: "BookOpen", Kind: KindRoute},
			{ID: "library", Label: "Library", Icon: "Library", Kind: KindRoute},
			// Lanes: no hill90 web equivalent (SPEC-shell.md). No lucide
			// icon backs it since nav-items.ts has no entry to lift one
			// from -- "Layers" is this repo's own choice, not a source
			// fidelity claim.
			{ID: "lanes", Label: "Lanes", Icon: "Layers", Kind: KindNative},
		},
		Groups: []Group{
			{
				ID:    "build",
				Label: "Build",
				Children: []Item{
					{ID: "skills", Label: "Skills", Icon: "Wrench", Kind: KindRoute},
					{ID: "workflows", Label: "Workflows", Icon: "Zap", Kind: KindRoute},
					{ID: "mcp-servers", Label: "MCP Servers", Icon: "Server", Kind: KindRoute},
				},
			},
			{
				ID:    "connect",
				Label: "Connect",
				Children: []Item{
					{ID: "connections", Label: "Connections", Icon: "Plug", Kind: KindRoute},
					// "models" (Icon "Cpu") REMOVED here, w5f.md: the full nav
					// walk (agent-tui#94) found it was still a permanent STUB,
					// and internal/connectors.View's own "-- models --"
					// section already renders the exact same data
					// (connectors.Load's AvailableModel catalog) a dedicated
					// route would show -- confirmed by reading that view
					// directly, not assumed. This is a deliberate, evidenced
					// departure from this file's own ui_fidelity=1:1 claim
					// against hill90-app's nav-items.ts (the same kind of
					// documented exception Lanes' own KindNative already is,
					// just in the other direction: Lanes ADDS a route the web
					// nav has none for, this REMOVES one this terminal client
					// cannot justify duplicating) -- a nav entry that will
					// never carry content of its own is worse than no entry
					// (w5f.md's own instruction).
					{ID: "storage", Label: "Storage", Icon: "HardDrive", Kind: KindRoute},
					{ID: "discord", Label: "Discord", Icon: "MessageSquare", Kind: KindRoute},
					{ID: "secrets", Label: "Secrets", Icon: "Shield", Kind: KindRoute},
				},
			},
			{
				ID:    "observe",
				Label: "Observe",
				Children: []Item{
					{ID: "usage", Label: "Usage", Icon: "BarChart3", Kind: KindRoute},
					{ID: "monitoring", Label: "Monitoring", Icon: "Activity", Kind: KindRoute},
				},
			},
			{
				ID:    "docs",
				Label: "Docs",
				Children: []Item{
					{ID: "api-docs", Label: "API Docs", Icon: "FileText", Kind: KindRoute},
					// URL is nav-items.ts's own href for this entry, read
					// from that file 2026-08-22:
					// `{ type: 'link', id: 'platform-docs', label: 'Platform
					// Docs', href: 'https://docs.hill90.com', icon:
					// ExternalLink, external: true }`. The Kind was always
					// right; what was missing was the destination itself,
					// so the shell had nothing to route to and fell through
					// to a stub (internal/external's package doc comment).
					{ID: "platform-docs", Label: "Platform Docs", Icon: "ExternalLink", Kind: KindExternal, URL: "https://docs.hill90.com"},
				},
			},
			{
				ID:    "admin",
				Label: "Admin",
				Children: []Item{
					{ID: "admin-services", Label: "Services", Icon: "Server", Kind: KindRoute},
					{ID: "admin-profiles", Label: "Profiles", Icon: "Box", Kind: KindRoute},
					{ID: "admin-users", Label: "Users", Icon: "Users", Kind: KindRoute},
					{ID: "dependencies", Label: "Dependencies", Icon: "Package", Kind: KindRoute},
					{ID: "settings", Label: "Settings", Icon: "Settings", Kind: KindRoute},
				},
			},
		},
	}
}

// Node is one entry in Flatten()'s traversal order: either a top-level
// Item (GroupID == ""), a group header (Group != nil, Item is the zero
// value), or a group's child Item (GroupID == the owning Group.ID).
// Flatten always returns every node regardless of a sidebar's own
// expanded/collapsed display state -- collapsing is model.go's rendering
// concern, not this package's data concern (SPEC-shell.md: S1 is "no
// rendering").
type Node struct {
	Item    Item
	Group   *Group
	GroupID string
}

// IsGroupHeader reports whether n is a group header entry rather than a
// navigable Item.
func (n Node) IsGroupHeader() bool { return n.Group != nil }

// Flatten walks t in on-screen order -- every top-level Item, then every
// Group with its header followed by its Children -- the order keyboard
// ↑/↓ traversal (S3) needs, and the order model.go's View renders in.
func (t Tree) Flatten() []Node {
	nodes := make([]Node, 0, len(t.Items)+len(t.Groups))
	for _, it := range t.Items {
		nodes = append(nodes, Node{Item: it})
	}
	for gi := range t.Groups {
		g := t.Groups[gi]
		nodes = append(nodes, Node{Group: &t.Groups[gi]})
		for _, child := range g.Children {
			nodes = append(nodes, Node{Item: child, GroupID: g.ID})
		}
	}
	return nodes
}

// ItemByID returns the Item with the given ID -- top-level or inside any
// group -- and whether one was found. internal/shell needs the whole Item
// (not just the id its own routing table is keyed by) for a KindExternal
// destination, whose Label and URL are the two things its pane shows.
func (t Tree) ItemByID(id string) (Item, bool) {
	for _, it := range t.Items {
		if it.ID == id {
			return it, true
		}
	}
	for _, g := range t.Groups {
		for _, child := range g.Children {
			if child.ID == id {
				return child, true
			}
		}
	}
	return Item{}, false
}

// GroupContaining returns the ID of the Group whose Children contains id,
// or "" if id is a top-level Item or names nothing in t. model.go's
// auto-expand (S2: "the group containing the active route auto-expands")
// is built on this.
func (t Tree) GroupContaining(id string) string {
	for _, g := range t.Groups {
		for _, child := range g.Children {
			if child.ID == id {
				return g.ID
			}
		}
	}
	return ""
}
