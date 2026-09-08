# A3 verification — existing paired changes

estate #1270 and dotfiles #346 merged by another lane during this session. Astra did not merge them.

/Users/jon/source/repos/Personal/agent-estate-lanes/run/p6-corpus-rename/corpus-before-rename.db: d53bdb9c5fdca60593855a54a52f5abd75342f609ca1d43375a5b953a395b3ac

/Users/jon/source/repos/Personal/agent-estate-lanes/run/p6-corpus-rename/corpus-after-rename.db: d53bdb9c5fdca60593855a54a52f5abd75342f609ca1d43375a5b953a395b3ac

Hook payload only (SQL NOT executed): `sqlite3 ~/corpus/corpus.sqlite3 "UPDATE items SET status=status"`

exit 2
```
BLOCKED by ledger-write-guard (agent-dotfiles#276)

this opens the live ledger.sqlite3 or corpus.sqlite3 directly instead of through cli.py/core.py or a read-only mode. Both are durable records (agent-supervisor AGENTS.md invariant 1: 'the ledger is the record; tmux is the screen') -- an ad hoc write bypasses the invariants their own writers enforce. Read it with sqlite3 -readonly or a file:...?mode=ro URI; write it only through the record's own API.

```

Hook payload only (SQL NOT executed): `sqlite3 -readonly ~/corpus/corpus.sqlite3 "select count(*) from items"`

exit 0
```

```

Hook payload only (SQL NOT executed): `sqlite3 ~/corpus/ledger.sqlite3 "UPDATE items SET status=status"`

exit 2
```
BLOCKED by ledger-write-guard (agent-dotfiles#276)

this opens the live ledger.sqlite3 or corpus.sqlite3 directly instead of through cli.py/core.py or a read-only mode. Both are durable records (agent-supervisor AGENTS.md invariant 1: 'the ledger is the record; tmux is the screen') -- an ad hoc write bypasses the invariants their own writers enforce. Read it with sqlite3 -readonly or a file:...?mode=ro URI; write it only through the record's own API.

```

Hook payload only (SQL NOT executed): `sqlite3 -readonly ~/corpus/ledger.sqlite3 "select count(*) from items"`

exit 0
```

```

corpus.sqlite3 positive read: exit 0, rows 7311

ledger.sqlite3 positive read: exit 0, rows 7311