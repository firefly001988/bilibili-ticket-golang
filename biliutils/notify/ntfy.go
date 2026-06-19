package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// NtfyNotifier sends notifications via an ntfy server.
// ntfy is a simple HTTP-based pub-sub notification service.
// See: https://ntfy.sh/
type NtfyNotifier struct {
	endpoint string // ntfy server URL, e.g. "https://ntfy.sh"
	topic    string // ntfy topic name
	token    string // optional access token for protected topics
}

// NewNtfy creates a new Ntfy notifier from a params map.
// Expected keys: "endpoint", "topic", "token" (optional).
func NewNtfy(params map[string]string) *NtfyNotifier {
	return &NtfyNotifier{
		endpoint: params["endpoint"],
		topic:    params["topic"],
		token:    params["token"],
	}
}

// Notify sends a message via ntfy.
func (n *NtfyNotifier) Notify(message string) (bool, string) {
	if n.topic == "" {
		return false, "Missing topic"
	}
	if n.endpoint == "" {
		return false, "Missing endpoint"
	}

	req, err := http.NewRequest("POST", n.endpoint+"/"+n.topic, strings.NewReader(message))
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")

	// ntfy
	req.Header.Set("Title", "Bili-Ticket-Go 抢票通知")
	req.Header.Set("Priority", "5") // max priority

	// If a token is provided, use Bearer auth
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}

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
		ErrorCode        int    `json:"http"`
		ErrorMessage     string `json:"errorMessage"`
		ErrorDescription string `json:"errorDescription"`
	}
	json.Unmarshal(respBody, &errorResp)
	if errorResp.Error != "" {
		return false, errorResp.Error + ": " + errorResp.ErrorDescription
	}
	return false, "HTTP " + resp.Status
}

// Test sends a test message.
func (n *NtfyNotifier) Test() (bool, string) {
	return n.Notify("Test message - 测试消息")
}
