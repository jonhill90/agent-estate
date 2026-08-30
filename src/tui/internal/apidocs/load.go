// Package apidocs is Docs -> API Docs' real pane: the estate's own
// generated API reference, read from the SAME OpenAPI document the web app
// renders and the API itself serves -- never a hand-written list of
// endpoints kept in this repo, which would drift the day an endpoint is
// added.
//
// The source, traced end to end rather than assumed (read 2026-08-22):
// hill90-app/services/api/src/openapi/openapi.yaml is tracked in git
// (`git ls-files` confirms; services/api/dist/openapi/openapi.yaml is
// build output and is not) and is loaded by two independent consumers
// there -- services/api/src/routes/docs.ts mounts it under swagger-ui-
// express, and services/ui/src/app/docs/api/page.tsx renders it through
// SwaggerClient pointed at /api/docs/openapi, which proxies the API's own
// /openapi.json. That web page is exactly the destination this nav route
// mirrors: hill90-app's nav-items.ts gives API Docs href '/docs/api'.
// Reading the YAML directly is this TUI's equivalent of that page, one
// hop closer to the source than the proxy is.
//
// This package parses; it does not render a schema browser. An OpenAPI
// document is far larger than a terminal pane (the spec above is ~5,600
// lines), so what is projected is the operation table -- method, path,
// whether the operation requires auth, and its own summary line -- which
// is the part a human reads to answer "what can this API do", and every
// field of it comes from the document rather than from this file.
package apidocs

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Reference is one parsed OpenAPI document, projected to what a terminal
// pane can honestly show. SourcePath is carried so the view can name the
// file it read -- a reference with no provenance on screen is one a reader
// cannot check.
type Reference struct {
	Title       string
	Version     string
	Description string
	OpenAPI     string
	SourcePath  string
	Endpoints   []Endpoint
	// PathCount is the number of distinct paths, which is NOT
	// len(Endpoints): one path carries several operations (/agents has
	// both get and post). Both figures are shown because a reader
	// comparing this pane against the spec would otherwise have to guess
	// which one "104" meant.
	PathCount int
}

// Endpoint is one operation. Auth is a three-state on purpose: OpenAPI's
// `security: []` on an operation means "explicitly public" and no
// `security` key at all means "inherits the document's own default",
// which are different claims -- collapsing them into a bool would make
// this pane assert something the document does not say (AGENTS.md's
// "absence is a typed value" convention).
type Endpoint struct {
	Method  string
	Path    string
	Summary string
	Tags    []string
	Auth    *bool
}

// methodOrder is the order operations on the same path are listed in --
// read order for a human (fetch, then create, then modify, then remove),
// not YAML document order, which varies path to path.
var methodOrder = []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}

func methodRank(m string) int {
	for i, want := range methodOrder {
		if m == want {
			return i
		}
	}
	return len(methodOrder)
}

// specDoc is the subset of OpenAPI this pane projects. Path items are held
// as raw nodes because a path item's keys are not all operations
// (`parameters`, `summary` and `$ref` are legal siblings of `get`); only
// the keys in methodOrder are decoded as operations, so a document using
// those siblings does not turn them into invented endpoints.
type specDoc struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title       string `yaml:"title"`
		Description string `yaml:"description"`
		Version     string `yaml:"version"`
	} `yaml:"info"`
	Paths map[string]map[string]yaml.Node `yaml:"paths"`
}

type operation struct {
	Summary  string       `yaml:"summary"`
	Tags     []string     `yaml:"tags"`
	Security *[]yaml.Node `yaml:"security"`
}

// Load reads and projects the OpenAPI document at path. An unreadable or
// unparseable file is an error naming the path -- the view renders that
// text verbatim rather than an empty table, because "we could not read the
// spec" and "the spec has no endpoints" are different facts.
func Load(path string) (Reference, error) {
	if strings.TrimSpace(path) == "" {
		return Reference{}, fmt.Errorf("no OpenAPI spec path configured")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Reference{}, fmt.Errorf("read %s: %w", path, err)
	}
	var doc specDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Reference{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.OpenAPI == "" && doc.Info.Title == "" && len(doc.Paths) == 0 {
		return Reference{}, fmt.Errorf("parse %s: not an OpenAPI document (no openapi/info/paths keys)", path)
	}

	ref := Reference{
		Title:       doc.Info.Title,
		Version:     doc.Info.Version,
		Description: doc.Info.Description,
		OpenAPI:     doc.OpenAPI,
		SourcePath:  path,
		PathCount:   len(doc.Paths),
	}

	for p, item := range doc.Paths {
		for method, node := range item {
			lower := strings.ToLower(method)
			if methodRank(lower) == len(methodOrder) {
				continue // not an operation: parameters, summary, $ref
			}
			var op operation
			if err := node.Decode(&op); err != nil {
				// One unparseable operation does not invalidate the rest;
				// it is listed with no summary rather than dropped, so the
				// endpoint count still matches the document.
				ref.Endpoints = append(ref.Endpoints, Endpoint{Method: strings.ToUpper(lower), Path: p})
				continue
			}
			ref.Endpoints = append(ref.Endpoints, Endpoint{
				Method:  strings.ToUpper(lower),
				Path:    p,
				Summary: strings.TrimSpace(op.Summary),
				Tags:    op.Tags,
				Auth:    authFor(op),
			})
		}
	}

	sort.Slice(ref.Endpoints, func(i, j int) bool {
		if ref.Endpoints[i].Path != ref.Endpoints[j].Path {
			return ref.Endpoints[i].Path < ref.Endpoints[j].Path
		}
		return methodRank(strings.ToLower(ref.Endpoints[i].Method)) <
			methodRank(strings.ToLower(ref.Endpoints[j].Method))
	})

	return ref, nil
}

// authFor reads OpenAPI's own three states off one operation: no security
// key (nil -- inherits the document default), an empty list (explicitly
// public), or a non-empty list (a scheme is required).
func authFor(op operation) *bool {
	if op.Security == nil {
		return nil
	}
	required := len(*op.Security) > 0
	return &required
}
