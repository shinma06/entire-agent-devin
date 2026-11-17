package devin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const testKey = "cog_test_key_value"

func fakeAPI(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		const child = "/v3/organizations/org-1/sessions/devin-" + childID
		switch r.URL.Path {
		case "/v3/self":
			_, _ = w.Write([]byte(`{"org_id":"org-1","principal_type":"service_user"}`))
		case child:
			_, _ = w.Write([]byte(`{"session_id":"` + childID + `","title":"Trivial ack","status":"exit","url":"https://app.devin.ai/sessions/` + childID + `","parent_session_id":"parent1","acus_consumed":1.5,"created_at":100,"updated_at":200,"pull_requests":[{"pr_url":"https://github.com/o/r/pull/1","pr_state":"open"}]}`))
		case child + "/messages":
			if r.URL.Query().Get("after") == "" {
				_, _ = w.Write([]byte(`{"items":[{"created_at":101,"event_id":"e1","message":"<received_chat_message from=\"x\">Reply ok</received_chat_message>","source":"user"}],"end_cursor":"c1","has_next_page":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":[{"created_at":102,"event_id":"e2","message":"ok, key ` + testKey + `","source":"devin"}],"end_cursor":null,"has_next_page":false}`))
		case "/v3/organizations/org-1/sessions/devin-forbidden":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"title":"Forbidden","detail":"no"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DEVIN_API_BASE", srv.URL)
	t.Setenv(APIKeyEnv, testKey)
	return srv
}

const childID = "a3e093f7f8b84af6bd645a2acbbc9790"

func TestImportWritesTranscriptWithoutKey(t *testing.T) {
	fakeAPI(t)
	repo := t.TempDir()
	var out bytes.Buffer
	if err := Import(repo, []string{childID}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), testKey) {
		t.Fatal("output leaks the API key")
	}
	data, err := os.ReadFile(SessionFile(SessionDir(repo), "devin-"+childID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), testKey) {
		t.Fatal("transcript leaks the API key")
	}
	recs := parseRecords(data)
	if len(recs) != 3 || recs[0].Source != importSource || recs[0].Devin.ACUsConsumed != 1.5 ||
		recs[0].Devin.ParentSessionID != "devin-parent1" || len(recs[0].Devin.PullRequests) != 1 {
		t.Fatalf("records = %+v", recs)
	}
	if recs[1].Kind != KindUser || recs[1].Prompt != "Reply ok" || recs[2].Kind != KindAssistant || recs[2].Message != "ok, key [REDACTED]" {
		t.Fatalf("messages = %+v %+v", recs[1], recs[2])
	}
	if _, err := compactTranscript(data); err != nil {
		t.Fatal(err)
	}
	// A second import refreshes the imported file.
	if err := Import(repo, []string{"devin-" + childID}, &out); err != nil {
		t.Fatal(err)
	}
}

func TestImportKeepsHookTranscript(t *testing.T) {
	fakeAPI(t)
	repo := t.TempDir()
	path := SessionFile(SessionDir(repo), "devin-"+childID)
	if err := appendRecord(path, Record{V: 1, Kind: KindSessionStart, SessionID: "devin-" + childID, Source: "startup"}); err != nil {
		t.Fatal(err)
	}
	if err := Import(repo, []string{childID}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "records from hooks") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportKeepsHookRecordsAppendedAfterImport(t *testing.T) {
	fakeAPI(t)
	repo := t.TempDir()
	if err := Import(repo, []string{childID}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	path := SessionFile(SessionDir(repo), "devin-"+childID)
	if err := appendRecord(path, Record{V: 1, Kind: KindTool, SessionID: "devin-" + childID, ToolName: "exec"}); err != nil {
		t.Fatal(err)
	}
	if err := Import(repo, []string{childID}, &bytes.Buffer{}); err == nil {
		t.Fatal("refresh overwrote a hook record")
	}
}

func TestSyncFailsWhenAChildFails(t *testing.T) {
	fakeAPI(t)
	repo := t.TempDir()
	resp, _ := json.Marshal("session_id=" + strings.Repeat("0", 32))
	if err := appendRecord(SessionFile(SessionDir(repo), "devin-p"), Record{V: 1, Kind: KindTool, SessionID: "devin-p", ToolName: SubagentTool, ToolResponse: resp}); err != nil {
		t.Fatal(err)
	}
	if err := Sync(repo, "devin-p", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "1 of 1") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportForbiddenNamesPermission(t *testing.T) {
	fakeAPI(t)
	err := Import(t.TempDir(), []string{"forbidden"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "ViewOrgSessions") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportNeedsKey(t *testing.T) {
	t.Setenv(APIKeyEnv, "")
	if err := Import(t.TempDir(), []string{childID}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), APIKeyEnv) {
		t.Fatalf("err = %v", err)
	}
}

func TestSyncImportsChildrenAndRecordsACUs(t *testing.T) {
	fakeAPI(t)
	repo := t.TempDir()
	parent := SessionFile(SessionDir(repo), "devin-parent1")
	resp, _ := json.Marshal("Created 1 session(s): session_id=" + childID)
	for _, r := range []Record{
		{V: 1, Kind: KindSessionStart, SessionID: "devin-parent1", Source: "startup"},
		{V: 1, Kind: KindTool, SessionID: "devin-parent1", ToolName: SubagentTool, ToolUseID: "tu1", ToolResponse: resp},
	} {
		if err := appendRecord(parent, r); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		var out bytes.Buffer
		if err := Sync(repo, "", &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "synced devin-"+childID) {
			t.Fatalf("out = %s", out.String())
		}
	}
	data, _ := os.ReadFile(parent)
	recs := parseRecords(data)
	if len(recs) != 3 || recs[2].Kind != KindSubagentSync || recs[2].Devin.SessionID != "devin-"+childID || recs[2].Devin.ACUsConsumed != 1.5 {
		t.Fatalf("parent records = %+v", recs)
	}
	if _, err := os.Stat(SessionFile(SessionDir(repo), "devin-"+childID)); err != nil {
		t.Fatal(err)
	}
}
