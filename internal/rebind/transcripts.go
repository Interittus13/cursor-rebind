package rebind

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Interittus13/cursor-rebind/internal/vscdb"
)

// ensureAgentTranscripts writes ~/.cursor/projects/<slug>/agent-transcripts/<id>/<id>.jsonl
// for migrate members that have composerData bubbles but no Agents transcript file.
// Agents Window hydrates history from these JSONL files; IDE uses cursorDiskKV bubbles.
func ensureAgentTranscripts(globalDB string, plan *Plan) (int, error) {
	if plan == nil || plan.Mode != ModeExact || plan.ProjectTo == "" {
		return 0, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	db, err := vscdb.OpenReadOnly(globalDB)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	headers := loadHeaderMap(db)
	written := 0
	for _, id := range composersNeedingGlassTo(plan, headers) {
		if composerIsEmpty(db, id, headers[id].Name) {
			continue
		}
		ok, err := writeAgentTranscriptIfMissing(db, home, plan.ProjectTo, id)
		if err != nil {
			return written, err
		}
		if ok {
			written++
		}
	}
	return written, nil
}

func writeAgentTranscriptIfMissing(db *sql.DB, home, projectSlug, composerID string) (bool, error) {
	dir := filepath.Join(home, ".cursor", "projects", projectSlug, "agent-transcripts", composerID)
	path := filepath.Join(dir, composerID+".jsonl")
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return false, nil
	}
	lines, err := bubblesToTranscriptLines(db, composerID)
	if err != nil || len(lines) == 0 {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	f, err := os.Create(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.Write(append(line, '\n')); err != nil {
			return false, err
		}
	}
	return true, nil
}

func bubblesToTranscriptLines(db *sql.DB, composerID string) ([][]byte, error) {
	raw, ok, err := vscdb.GetDiskKVRaw(db, "composerData:"+composerID)
	if err != nil || !ok {
		return nil, err
	}
	var blob map[string]any
	if json.Unmarshal(raw, &blob) != nil {
		return nil, nil
	}
	headers, _ := blob["fullConversationHeadersOnly"].([]any)
	if len(headers) == 0 {
		return nil, nil
	}
	var out [][]byte
	for _, item := range headers {
		h, _ := item.(map[string]any)
		if h == nil {
			continue
		}
		bid, _ := h["bubbleId"].(string)
		if bid == "" {
			continue
		}
		braw, bok, err := vscdb.GetDiskKVRaw(db, "bubbleId:"+composerID+":"+bid)
		if err != nil || !bok {
			continue
		}
		var bubble map[string]any
		if json.Unmarshal(braw, &bubble) != nil {
			continue
		}
		role := "assistant"
		if t, _ := bubble["type"].(float64); int(t) == 1 {
			role = "user"
		}
		content := bubbleContentParts(bubble)
		if len(content) == 0 {
			continue
		}
		line, err := json.Marshal(map[string]any{
			"role": role,
			"message": map[string]any{
				"content": content,
			},
		})
		if err != nil {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

func bubbleContentParts(bubble map[string]any) []map[string]any {
	var parts []map[string]any
	if think, _ := bubble["thinking"].(map[string]any); think != nil {
		if t, _ := think["text"].(string); t != "" {
			parts = append(parts, map[string]any{"type": "text", "text": t})
		}
	}
	if t, _ := bubble["text"].(string); t != "" {
		parts = append(parts, map[string]any{"type": "text", "text": t})
	}
	return parts
}
