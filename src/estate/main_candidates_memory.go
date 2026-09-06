package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jonhill90/agent-estate/estate/internal/candidates"
	"github.com/jonhill90/agent-estate/estate/internal/corpus"
	"github.com/jonhill90/agent-estate/estate/internal/livepath"
)

func runCandidatesMemory(args []string) {
	fs := flag.NewFlagSet("candidates memory", flag.ExitOnError)
	db := fs.String("db", "", "corpus path")
	id := fs.String("id", "", "candidate id")
	roster := fs.String("roster", "", "canonical agent roster path for roster-link")
	reviewer := fs.String("reviewer", "", "explicit reviewer actor for MOC decisions")
	action := fs.String("action", "show", "propose, show, accept, reject, moc-propose, moc-accept, moc-reject, refresh")
	proposal := fs.String("proposal", "", "private proposal JSON file")
	vault := fs.String("vault", os.Getenv("AGENT_MEMORY_VAULT"), "Agent Memory vault root")
	apply := fs.Bool("apply", false, "apply reviewed change (default validates only)")
	authorized := fs.Bool("authorized-live-write", false, "acknowledge live corpus write")
	repoPath := fs.String("repo-path", "", "for a repo-destination accept: the exact path the patch was integrated at")
	repoCommit := fs.String("repo-commit", "", "for a repo-destination accept: the commit SHA that integrated it")
	fs.Parse(args)
	fail := func(err error) { fmt.Fprintln(os.Stderr, "estate:", err); os.Exit(1) }
	if strings.HasPrefix(*action, "moc-") || *action == "refresh" || *action == "roster-link" {
		var out any
		var err error
		switch *action {
		case "roster-link":
			if *apply {
				err = candidates.WriteRosterPointer(*vault, *roster)
			} else {
				_, err = os.Stat(*roster)
			}
			out = map[string]bool{"applied": *apply && err == nil}
		case "moc-propose":
			out, err = candidates.MOCProposals(*vault, *apply)
		case "moc-accept", "moc-reject":
			err = candidates.ReviewMOC(*vault, *id, *reviewer, *action == "moc-accept", *apply)
			out = map[string]bool{"applied": *apply && err == nil}
		case "refresh":
			out, err = candidates.RefreshMOCs(*vault, *apply)
		default:
			err = fmt.Errorf("unknown action")
		}
		if err != nil {
			fail(err)
		}
		json.NewEncoder(os.Stdout).Encode(out)
		return
	}
	if fs.NArg() != 0 || *id == "" {
		fail(fmt.Errorf("-id required; unexpected positional arguments are refused"))
	}
	if *db == "" {
		var err error
		*db, err = corpus.Path()
		if err != nil {
			fail(err)
		}
	}
	if *apply && *action != "show" {
		if reason, live := livepath.RefuseLivePath(*db); live && !*authorized {
			fail(fmt.Errorf("%s; use -authorized-live-write", reason))
		}
	}
	var r candidates.MemoryReview
	var err error
	switch *action {
	case "show":
		r, err = candidates.ReadMemory(*db, *id)
	case "propose":
		var f *os.File
		f, err = os.Open(*proposal)
		if err == nil {
			var p candidates.Proposal
			dec := json.NewDecoder(f)
			dec.DisallowUnknownFields()
			err = dec.Decode(&p)
			f.Close()
			if err == nil {
				r, err = candidates.Propose(*db, *id, p, false)
				if err == nil {
					err = candidates.StageMemory(*vault, *id, r, false)
				}
				if err == nil {
					r, err = candidates.Propose(*db, *id, p, *apply)
				}
				if err == nil {
					err = candidates.StageMemory(*vault, *id, r, *apply)
				}
			}
		}
	case "accept", "reject":
		// Route by the SAVED proposal's own declared destination_kind
		// (deliverable 3: acceptance either publishes through the existing
		// memory mechanism or records a repo/docs/skill patch receipt) --
		// never by a flag the caller could set inconsistently with what was
		// actually proposed and reviewed.
		var existing candidates.MemoryReview
		existing, err = candidates.ReadMemory(*db, *id)
		if err == nil && existing.Proposal.DestinationKind == "repo" {
			r, err = candidates.PublishRepo(*db, *id, *action, *repoPath, *repoCommit, *apply)
		} else if err == nil {
			r, err = candidates.Publish(*db, *vault, *id, *action, *apply)
		}
	default:
		err = fmt.Errorf("unknown memory action %q", *action)
	}
	if err != nil {
		fail(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(r); err != nil {
		fail(err)
	}
}
