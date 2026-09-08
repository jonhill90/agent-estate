# A2 — retirement stopped at content verification

A1 completed and all eight batches validated. Before dissolution, inspection found four hidden candidate backup directories plus a candidate lock in agent/, beyond the brief’s described empty facts/index/corpus/signpost set. These include prior canonical files and indexes. They were preserved. The safe-deletion gate forbids deleting a target whose contents differ from its description. No corpus extraction move, index move, or agent removal was attempted.

```json
[
  {
    "path": ".candidate-memory.lock",
    "files": 1,
    "bytes": 0,
    "children": []
  },
  {
    "path": ".inmaps-backup-4033725576",
    "files": 1,
    "bytes": 1471,
    "children": [
      "0"
    ]
  },
  {
    "path": ".inmaps-backup-869119760",
    "files": 2,
    "bytes": 12105,
    "children": [
      "0",
      "1"
    ]
  },
  {
    "path": ".memory-backup-1729756205",
    "files": 3,
    "bytes": 34876,
    "children": [
      "1-index.md",
      "0-harness-memory-override-is-later.md",
      "2-log.md"
    ]
  },
  {
    "path": ".memory-backup-2653422664",
    "files": 2,
    "bytes": 31341,
    "children": [
      "1-index.md",
      "2-log.md"
    ]
  },
  {
    "path": "00 - Inbox",
    "files": 1,
    "bytes": 2774,
    "children": [
      "README.md"
    ]
  },
  {
    "path": "INDEX-CONTRACT.md",
    "files": 1,
    "bytes": 6104,
    "children": []
  },
  {
    "path": "corpus",
    "files": 2,
    "bytes": 1101361,
    "children": [
      "prompts.jsonl",
      "README.md"
    ]
  },
  {
    "path": "facts",
    "files": 0,
    "bytes": 0,
    "children": []
  },
  {
    "path": "index.md",
    "files": 1,
    "bytes": 19139,
    "children": []
  }
]
```

Gate: FAIL — agent/ still exists. Move receipt: UNRUN. Required next iteration: select a preserved private backup destination for these additional artifacts and deploy/repoint all readers before retiring agent/. Main still requires agent/facts; this push’s reader fix is unmerged.
