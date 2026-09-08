# Knowledge

How the estate remembers what the operator decided, and how an agent asks it.
Linked from README.md, which keeps only a pointer -- "the README reads like a
dissertation; cut it back to a short entry point that links onward"
(corpus it-91dc4028627a97d, hard).

The estate remembers what the operator has decided, and an agent can ask it.
Three layers, and the distinction between them is the whole design:

```
1  evidence   ~/corpus/corpus.sqlite3     10,371 prompts -> 7,890 judged items
2  knowledge  $AGENT_MEMORY_VAULT         3,217 evidence notes, 352 rules
3  views      estate knowledge            a compiled index, regenerable
```

**Layer 1** is the record: every prompt, with the context it was said in.
Immutable. `text_raw` is never published; `text_clean` is the quotable form.

**Layer 2** is the Obsidian vault at `$AGENT_MEMORY_VAULT` — one env var, set
by `agent-dotfiles/install.sh`, which every consumer reads. Nothing hardcodes
a path. Inside it, `01 - Notes/01p - Parameters` holds one note per thing the
operator said, each carrying its own context and provenance;
`01 - Notes/01f - Facts` holds the *rules* distilled from them; `02 - MOCs`
routes by subject, leading with rules and then citing the evidence.

**A rule is never composed.** Its statement is verbatim from a corpus item, so
the facts layer cannot contain a sentence the operator did not cause to be
written. Selection is judgement — which of forty statements on a subject
binds — and two heuristics failed at it before a human-read pass replaced
them: term centrality returns the most *average* statement, and
imperative-word matching returns whichever rule happens to contain "never".
`internal/distill` is the mechanical half; its own probe measured the ceiling
of the lexical approach at 3% coverage and says so.

```
estate knowledge                      rebuild the compiled index
estate knowledge query "..."          ask it; --private includes private items
estate knowledge query "#memory"      scoped to one subject
estate vault-view                     regenerate projections from the corpus
estate candidates memory -action ...  the only sanctioned write path
```

Retrieval ranks a distilled rule above the evidence it came from — 3,217
evidence notes otherwise outvote 352 rules on population alone, and
"can I commit directly to main" returned a note about chatting with the
director because it contained "directly".

**Point `ESTATE_KNOWLEDGE_INDEX` at a private path** before rebuilding. The
shared index is never regenerated casually.

**Limits, stated rather than discovered:** retrieval is lexical, so a question
phrased differently from the rule can miss. The rules layer is a first pass,
unevenly covering 45 subjects, unreviewed by a second reader. And the vault is
not in git — iCloud produced 484 conflict copies in a single session on
2026-09-07. Snapshots live under `~/.local/state/agent-estate/recovery/`.
