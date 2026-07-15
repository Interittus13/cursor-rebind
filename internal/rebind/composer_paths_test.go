package rebind

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestEnrichTrackedGitReposAddsRepoURL(t *testing.T) {
	to := "/tmp/to-proj"
	in := []any{map[string]any{"repoPath": to}}
	out := enrichTrackedGitRepos(in, to, "github-rittus/interittus13/cursor-rebind")
	if len(out) != 1 {
		t.Fatalf("len=%d", len(out))
	}
	m := out[0].(map[string]any)
	if m["repoUrl"] != "github-rittus/interittus13/cursor-rebind" {
		t.Fatalf("%v", m)
	}
	if _, ok := m["branches"]; !ok {
		t.Fatalf("missing branches: %v", m)
	}
}

func TestNormalizeTrackedGitReposAddsBranches(t *testing.T) {
	in := []any{map[string]any{"repoPath": "/tmp/x"}}
	out := normalizeTrackedGitRepos(in)
	m := out[0].(map[string]any)
	br, ok := m["branches"].([]any)
	if !ok || br == nil {
		t.Fatalf("branches=%v", m["branches"])
	}
}

func TestRemapTrackedGitReposMergesFromIntoTo(t *testing.T) {
	from := "/home/ulap92/Documents/Arpit/_Others/GitHub/mover"
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/cursor-rebind"
	in := []any{
		map[string]any{"repoPath": to, "branchName": "feat/migrate"},
		map[string]any{"repoPath": from, "branchName": "main"},
	}
	out := remapTrackedGitRepos(in, from, to, ModeExact)
	if len(out) != 1 {
		t.Fatalf("want 1 merged entry, got %d: %#v", len(out), out)
	}
	m := out[0].(map[string]any)
	if filepath.Clean(m["repoPath"].(string)) != filepath.Clean(to) {
		t.Fatalf("repoPath=%v", m["repoPath"])
	}
}

func TestRewriteEmbeddedPathExact(t *testing.T) {
	from := "/home/ulap92/Documents/Arpit/_Others/GitHub/mover"
	to := "/home/ulap92/Documents/Arpit/_Others/GitHub/cursor-rebind"
	got := rewriteEmbeddedPath(from+"/internal/rebind/rebind.go", from, to, ModeExact)
	want := to + "/internal/rebind/rebind.go"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewriteValuePathsMapKeys(t *testing.T) {
	from := "/tmp/from-proj"
	to := "/tmp/to-proj"
	blob := map[string]any{
		"originalFileStates": map[string]any{
			"file://"+from+"/a.go": map[string]any{"content": "x"},
		},
		"trackedGitRepos": []any{
			map[string]any{"repoPath": from},
			map[string]any{"repoPath": to},
		},
	}
	raw, _ := json.Marshal(rewriteValuePaths(blob, from, to, ModeExact))
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	ofs := out["originalFileStates"].(map[string]any)
	if _, ok := ofs["file://"+to+"/a.go"]; !ok {
		t.Fatalf("keys=%v", ofs)
	}
	merged := remapTrackedGitRepos(out["trackedGitRepos"], from, to, ModeExact)
	if len(merged) != 1 {
		t.Fatalf("merged=%d", len(merged))
	}
}
