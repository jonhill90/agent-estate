package knowledgeindex

import (
	"encoding/json"
	"os"
	"testing"
)

func writeFixture(t *testing.T, path string, res Result) {
	t.Helper()
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
