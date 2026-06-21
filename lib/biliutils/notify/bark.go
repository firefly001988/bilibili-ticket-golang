package notify

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// BarkNotifier sends notifications via the Bark app.
type BarkNotifier struct {
	endpoint string
	token    string
}

func NewBark(params map[string]string) *BarkNotifier {
	return &BarkNotifier{
		endpoint: params["endpoint"],
		token:    params["token"],
	}
}

func (b *BarkNotifier) Notify(message string) (bool, string) {
	if b.token == "" || b.endpoint == "" {
		return false, "Missing token or endpoint"
	}
	body := map[string]any{
		"title":  "Bili-Ticket-Go 抢票通知",
		"body":   message,
		"level":  "critical",
		"call":   1,
		"volume": 10,
	}
	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", b.endpoint+"/"+b.token, bytes.NewBuffer(jsonBody))
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
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	json.Unmarshal(respBody, &errorResp)
	return false, errorResp.Message
}

func (b *BarkNotifier) Test() (bool, string) {
	return b.Notify("Test message - 测试消息")
}
