package hookstatus

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNotFound marks a GitHub 404 -- distinct from every other failure mode
// (auth, rate limit, network) so a caller can tell "this really doesn't
// exist upstream" from "could not ask" and never conflate the two, the
// same honest-absence discipline internal/knowledge's SourceResult uses.
var ErrNotFound = errors.New("not found on GitHub")

// GH is the real Upstream, shelling to `gh api` -- this repo's CLI-first
// convention (see AGENTS.md's Operator Parameters, and internal/gate's
// identical pattern for GitHub reads). Every call here is a GET; none of
// them can write to the repository this package inspects.
type GH struct{}

func (GH) MainSHA(repo string) (string, error) {
	out, err := exec.Command("gh", "api", "repos/"+repo+"/git/refs/heads/main", "--jq", ".object.sha").Output()
	if err != nil {
		return "", fmt.Errorf("gh api repos/%s/git/refs/heads/main: %w", repo, wrapGHErr(err))
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("gh api repos/%s/git/refs/heads/main returned an empty sha", repo)
	}
	return sha, nil
}

type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

func (GH) Tree(repo, ref, prefix string) (map[string]string, error) {
	out, err := exec.Command("gh", "api", "repos/"+repo+"/git/trees/"+ref+"?recursive=1", "--jq", ".tree").Output()
	if err != nil {
		return nil, fmt.Errorf("gh api repos/%s/git/trees/%s: %w", repo, ref, wrapGHErr(err))
	}
	var entries []treeEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("decode tree for repos/%s/git/trees/%s: %w", repo, ref, err)
	}
	blobs := map[string]string{}
	for _, e := range entries {
		if e.Type == "blob" && strings.HasPrefix(e.Path, prefix) {
			blobs[e.Path] = e.SHA
		}
	}
	return blobs, nil
}

func (GH) File(repo, ref, path string) ([]byte, error) {
	out, err := exec.Command("gh", "api", "repos/"+repo+"/contents/"+path+"?ref="+ref, "--jq", ".content").Output()
	if err != nil {
		wrapped := wrapGHErr(err)
		if strings.Contains(strings.ToLower(wrapped.Error()), "404") {
			return nil, fmt.Errorf("%s@%s: %w", path, ref, ErrNotFound)
		}
		return nil, fmt.Errorf("gh api repos/%s/contents/%s@%s: %w", repo, path, ref, wrapped)
	}
	b64 := strings.TrimSpace(string(out))
	return decodeGitHubContent(b64)
}

func (GH) CommitExists(repo, sha string) (bool, error) {
	out, err := exec.Command("gh", "api", "repos/"+repo+"/commits/"+sha, "--jq", ".sha").Output()
	if err != nil {
		msg := strings.ToLower(string(wrapGHErr(err).Error()))
		if strings.Contains(msg, "404") || strings.Contains(msg, "422") || strings.Contains(msg, "no commit found") {
			return false, nil
		}
		return false, fmt.Errorf("gh api repos/%s/commits/%s: %w", repo, sha, wrapGHErr(err))
	}
	return strings.TrimSpace(string(out)) == sha, nil
}

// wrapGHErr folds an *exec.ExitError's own stderr into the error text --
// `gh api`'s failure reason (404, auth, rate limit) lives there, not in
// the Go error alone.
func wrapGHErr(err error) error {
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}

// decodeGitHubContent decodes the GitHub Contents API's own base64 body,
// which is newline-wrapped at 60 chars -- encoding/base64 refuses embedded
// newlines, so they are stripped first.
func decodeGitHubContent(b64 string) ([]byte, error) {
	b64 = strings.ReplaceAll(b64, "\n", "")
	return base64.StdEncoding.DecodeString(b64)
}
