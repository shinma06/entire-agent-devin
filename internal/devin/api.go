package devin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// APIKeyEnv names the variable holding the Devin API key used by sync and import.
const APIKeyEnv = "DEVIN_API_KEY"

const defaultAPIBase = "https://api.devin.ai"

// apiClient reads sessions through the Devin v3 API. It never prints the key and
// scrubs it from everything it returns.
type apiClient struct {
	base, key, org string
	http           *http.Client
}

// SessionInfo is the session metadata kept in imported and synced records.
type SessionInfo struct {
	SessionID       string   `json:"session_id"`
	Title           string   `json:"title,omitempty"`
	Status          string   `json:"status,omitempty"`
	URL             string   `json:"url,omitempty"`
	ParentSessionID string   `json:"parent_session_id,omitempty"`
	ACUsConsumed    float64  `json:"acus_consumed"`
	PullRequests    []string `json:"pull_requests,omitempty"`
	CreatedAt       int64    `json:"created_at,omitempty"`
	UpdatedAt       int64    `json:"updated_at,omitempty"`
}

type apiMessage struct {
	CreatedAt int64  `json:"created_at"`
	EventID   string `json:"event_id"`
	Message   string `json:"message"`
	Source    string `json:"source"`
}

func newAPIClient() (*apiClient, error) {
	key := strings.TrimSpace(os.Getenv(APIKeyEnv))
	if key == "" {
		return nil, fmt.Errorf("%s is not set; bind the Devin secret to this command", APIKeyEnv)
	}
	base := strings.TrimRight(os.Getenv("DEVIN_API_BASE"), "/")
	if base == "" {
		base = defaultAPIBase
	}
	c := &apiClient{base: base, key: key, http: &http.Client{Timeout: 30 * time.Second}}
	var self struct {
		OrgID string `json:"org_id"`
	}
	if err := c.get("/v3/self", &self); err != nil {
		return nil, err
	}
	if self.OrgID == "" {
		return nil, errors.New("devin api: /v3/self returned no org_id; use an organization service user key")
	}
	c.org = self.OrgID
	return c, nil
}

func (c *apiClient) get(path string, v any) error {
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("devin api: GET %s: %s", path, c.scrub(err.Error()))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(body, &problem)
		msg := strings.TrimSpace(problem.Title + ": " + problem.Detail)
		if resp.StatusCode == http.StatusForbidden {
			msg += " (the service user needs a role that can view organization sessions, ViewOrgSessions)"
		}
		return fmt.Errorf("devin api: GET %s: %d %s", path, resp.StatusCode, c.scrub(msg))
	}
	return json.Unmarshal([]byte(c.scrub(string(body))), v)
}

// scrub removes the API key from text that came back from the API or an error.
func (c *apiClient) scrub(s string) string {
	if c.key == "" {
		return s
	}
	return strings.ReplaceAll(s, c.key, "[REDACTED]")
}

func apiSessionID(id string) string {
	return "devin-" + strings.TrimPrefix(strings.TrimSpace(id), "devin-")
}

func (c *apiClient) session(id string) (SessionInfo, error) {
	var s struct {
		SessionInfo
		PullRequests []struct {
			URL string `json:"pr_url"`
		} `json:"pull_requests"`
	}
	if err := c.get("/v3/organizations/"+c.org+"/sessions/"+url.PathEscape(apiSessionID(id)), &s); err != nil {
		return SessionInfo{}, err
	}
	info := s.SessionInfo
	info.SessionID = apiSessionID(info.SessionID)
	if info.ParentSessionID != "" {
		info.ParentSessionID = apiSessionID(info.ParentSessionID)
	}
	for _, pr := range s.PullRequests {
		if pr.URL != "" {
			info.PullRequests = append(info.PullRequests, pr.URL)
		}
	}
	return info, nil
}

func (c *apiClient) messages(id string) ([]apiMessage, error) {
	var all []apiMessage
	after := ""
	for {
		q := url.Values{"first": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		var page struct {
			Items       []apiMessage `json:"items"`
			EndCursor   *string      `json:"end_cursor"`
			HasNextPage bool         `json:"has_next_page"`
		}
		if err := c.get("/v3/organizations/"+c.org+"/sessions/"+url.PathEscape(apiSessionID(id))+"/messages?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if !page.HasNextPage || page.EndCursor == nil || *page.EndCursor == "" || *page.EndCursor == after {
			return all, nil
		}
		after = *page.EndCursor
	}
}
