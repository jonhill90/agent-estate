# healthtick.sh

Event-driven watcher behind the Director's stall backstop: polls estate/PR state and exits the moment it changes -- exiting **is** the wake signal, by design, not a bug.

agent-estate#1248: nothing re-arms it afterward except a Director turn noticing -- out of scope here, tracked there.

Live copy that actually runs: `run/tools/healthtick.sh` in `agent-estate-lanes` (untracked, agent-estate#1332). This is the versioned copy, kept in sync with it, never the other way around.
