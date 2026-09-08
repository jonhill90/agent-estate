# Agent Memory vault validator

`validate_index.py` checks the vault's `index.md` contract: every bullet
resolves to a real note, every note has required frontmatter, no note is
orphaned, and (check 3a) every Markdown link anywhere in the vault
resolves to a file that exists.

The vault's live copy, at `$AGENT_MEMORY_VAULT/99 - Meta/tools/validate_index.py`,
is what actually runs. This is the versioned copy — kept in sync with it,
never the other way around. `test_validate_index.py` covers it with
fixtures; run with `python3 -m unittest scripts.knowledge.vault.test_validate_index`.
