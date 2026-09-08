# A5 — dual repository pointers

Five existing repo entries updated twice through sourcecatalogue register. All 29 pre-existing ID→Locator mappings unchanged. Verified remote URLs, local paths, checkout states and routing files are in push3-a5-evidence.json. Generated views refreshed through the CLI; Start Here links all five. Backups/checksums: push3-a5-backup/.

```
go test ./src/estate/internal/catalogue -count=1
ok github.com/jonhill90/agent-estate/estate/internal/catalogue 7.890s
go test ./src/estate/internal/knowledge -run Catalogue -count=1
ok github.com/jonhill90/agent-estate/estate/internal/knowledge 0.222s
go vet ./src/estate/internal/catalogue ./src/estate/cmd/sourcecatalogue
exit 0
```

Missing-local mutation failed (exit 1), restored code passes; full failure in push3-a5-mutation.txt. First refresh command put flags after its positional ID and was refused (exit 2); corrected flag order exited 0 and wrote 29 generated views. No source content or shared knowledge index was changed.
