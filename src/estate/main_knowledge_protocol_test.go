package main

import (
	"strings"
	"testing"
)

func TestKnowledgeReadProtocolIsMandatory(t *testing.T) {
	g := knowledgeGrounding()
	for _, s := range []string{"Before reasoning or implementation", "Start Here.md", "canonical source", "unavailable"} {
		if !strings.Contains(g, s) {
			t.Fatalf("read protocol missing %q", s)
		}
	}
}
