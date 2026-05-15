package notify

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// GotifyNotifier sends notifications via a Gotify server.
type GotifyNotifier struct {
	endpoint string
	token    string
}

// NewGotify creates a new Gotify notifier from a params map.
// Expected keys: "endpoint", "token".
func NewGotify(params map[string]string) *GotifyNotifier {
	return &GotifyNotifier{
		endpoint: params["endpoint"],
		token:    params["token"],
	}
}

// Notify sends a message via Gotify.
func (g *GotifyNotifier) Notify(message string) (bool, string) {
	if g.token == "" || g.endpoint == "" {
		return false, "Missing token or endpoint"
	}
	body := map[string]any{
		"title":    "Bili-Ticket-Go 抢票通知",
		"message":  message,
		"priority": 10,
	}
	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", g.endpoint+"/message?token="+g.token, bytes.NewBuffer(jsonBody))
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return true, ""
	}
	respBody, _ := io.ReadAll(resp.Body)
	var errorResp struct {
		Error            string `json:"error"`
		ErrorCode        int    `json:"errorCode"`
		ErrorDescription string `json:"errorDescription"`
	}
	json.Unmarshal(respBody, &errorResp)
	return false, errorResp.Error + ": " + errorResp.ErrorDescription
}

// Test sends a test message.
func (g *GotifyNotifier) Test() (bool, string) {
	return g.Notify("Test message - 测试消息")
}
