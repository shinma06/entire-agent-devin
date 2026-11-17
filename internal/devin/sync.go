package devin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// importSource marks transcripts written by import, so a later import may refresh them
// while transcripts recorded by hooks are never overwritten.
const importSource = "devin-api"

// Import writes each session's chat history from the Devin API as a native transcript in
// repoRoot's session directory, where `entire session attach <id> --agent devin` finds it.
func Import(repoRoot string, ids []string, w io.Writer) error {
	if len(ids) == 0 {
		return errors.New("import: pass one or more Devin session IDs")
	}
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	for _, id := range ids {
		info, path, err := importSession(c, repoRoot, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "imported %s (%s, %.2f ACUs) -> %s\n  attach: entire session attach %s --agent %s\n",
			info.SessionID, info.Status, info.ACUsConsumed, path, info.SessionID, AgentName)
	}
	return nil
}

func importSession(c *apiClient, repoRoot, id string) (SessionInfo, string, error) {
	info, err := c.session(id)
	if err != nil {
		return info, "", err
	}
	msgs, err := c.messages(id)
	if err != nil {
		return info, "", err
	}
	path := SessionFile(SessionDir(repoRoot), info.SessionID)
	data, err := importedTranscript(info, msgs)
	if err != nil {
		return info, "", err
	}
	return info, path, replaceImported(path, data)
}

// replaceImported writes an imported transcript under the session lock hooks use, and
// only over a file whose every record came from an import.
func replaceImported(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	lock, err := os.OpenFile(strings.TrimSuffix(path, ".jsonl")+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	unlock, err := lockFile(lock)
	if err != nil {
		return err
	}
	defer unlock()
	if old, err := os.ReadFile(path); err == nil {
		for _, r := range parseRecords(old) {
			if r.Source != importSource {
				return fmt.Errorf("import: %s has records from hooks; not overwriting it", path)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".import-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func importedTranscript(info SessionInfo, msgs []apiMessage) ([]byte, error) {
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].CreatedAt < msgs[j].CreatedAt })
	start := info.CreatedAt
	if start == 0 && len(msgs) > 0 {
		start = msgs[0].CreatedAt
	}
	recs := []Record{{V: 1, TS: unixTS(start), Kind: KindSessionStart, SessionID: info.SessionID, Source: importSource, Devin: &info}}
	for _, m := range msgs {
		rec := Record{V: 1, TS: unixTS(m.CreatedAt), SessionID: info.SessionID, Source: importSource}
		switch m.Source {
		case "user":
			rec.Kind, rec.PromptID, rec.Prompt = KindUser, m.EventID, cleanPrompt(m.Message)
		case "devin":
			rec.Kind, rec.ToolUseID, rec.Message = KindAssistant, m.EventID, m.Message
		default:
			continue
		}
		recs = append(recs, rec)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func unixTS(sec int64) string {
	if sec == 0 {
		return now().Format(time.RFC3339Nano)
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339Nano)
}

// Sync imports every child session the given session started, and records each child's
// status, ACUs and pull requests in the session's own transcript so the next checkpoint
// carries them. The session defaults to the most recently written transcript.
func Sync(repoRoot, sessionID string, w io.Writer) error {
	dir := SessionDir(repoRoot)
	ref := ""
	if sessionID != "" {
		ref = SessionFile(dir, apiSessionID(sessionID))
	} else {
		var err error
		if ref, err = latestTranscript(dir); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(ref)
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	recs := parseRecords(data)
	children := childSessions(recs)
	if len(children) == 0 {
		fmt.Fprintf(w, "sync: %s started no child sessions\n", filepath.Base(ref))
		return nil
	}
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	parent := ""
	if len(recs) > 0 {
		parent = recs[0].SessionID
	}
	failed := 0
	for _, child := range children {
		info, path, err := importSession(c, repoRoot, child)
		if err != nil {
			fmt.Fprintf(w, "sync: %s: %v\n", child, err)
			failed++
			continue
		}
		rec := Record{V: 1, TS: now().Format(time.RFC3339Nano), Kind: KindSubagentSync, SessionID: parent, Devin: &info}
		key := fmt.Sprintf("sync|%s|%s|%.4f|%d|%d", info.SessionID, info.Status, info.ACUsConsumed, len(info.PullRequests), info.UpdatedAt)
		if _, err := recordOnce(ref, key, rec); err != nil {
			return err
		}
		fmt.Fprintf(w, "synced %s (%s, %.2f ACUs, %d PRs) -> %s\n", info.SessionID, info.Status, info.ACUsConsumed, len(info.PullRequests), path)
	}
	if failed > 0 {
		return fmt.Errorf("sync: %d of %d child sessions failed", failed, len(children))
	}
	return nil
}

func latestTranscript(dir string) (string, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	best, bestTime := "", time.Time{}
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		if recs := parseRecords(data); len(recs) > 0 && recs[0].Source == importSource {
			continue
		}
		if st, err := os.Stat(m); err == nil && st.ModTime().After(bestTime) {
			best, bestTime = m, st.ModTime()
		}
	}
	if best == "" {
		return "", fmt.Errorf("sync: no session transcript in %s; pass --session-id", dir)
	}
	return best, nil
}

// childSessions lists the child session IDs in the responses of SubagentTool calls.
func childSessions(recs []Record) []string {
	var ids []string
	seen := map[string]bool{}
	for _, r := range recs {
		if r.Kind != KindTool || r.ToolName != SubagentTool {
			continue
		}
		for _, id := range childSessionIDs(r.ToolResponse) {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}
