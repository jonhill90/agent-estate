# Docs group captures — nav walk rows 18 and 19

Frames produced by `testdata/vhs/docs.tape` on 2026-08-22, when
`Docs -> API Docs` and `Docs -> Platform Docs` stopped being stubs.

`testdata/vhs/out/` is gitignored, so these four are copied here to be
citable from a PR body and a code review. They are evidence of one dated
run, not a golden file: nothing compares against them, and re-running the
tape is what re-measures those rows (`testdata/vhs/full-nav-walk-report.md`).

| file | what it shows |
|---|---|
| `18-docs-api-docs.png` | the real OpenAPI document: `Hill90 API v0.1.0`, its source path, 148 operations across 104 paths |
| `18b-docs-api-docs-filtered.png` | `[/]` filtering the same table to `agents/{id}` — 27 of 148 shown |
| `18c-docs-api-docs-unconfigured.png` | no `$HILL90_APP_REPO`/`-openapi`: the pane names the document it would read instead of falling back to a placeholder |
| `19-docs-platform-docs.png` | the external destination — the URL, that it opens in a browser, and `[o]` |
