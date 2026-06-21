package notify

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

type PushplusNotifier struct {
	token string
}

func NewPushplus(params map[string]string) *PushplusNotifier {
	return &PushplusNotifier{
		token: params["token"],
	}
}

func (p *PushplusNotifier) Notify(message string) (bool, string) {
	if p.token == "" {
		return false, "Missing token"
	}
	body := map[string]any{
		"token":   p.token,
		"title":   "Bili-Ticket-Go 抢票通知",
		"content": message,
	}
	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", "https://www.pushplus.plus/send", bytes.NewBuffer(jsonBody))
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
		Message string `json:"msg"`
	}
	json.Unmarshal(respBody, &errorResp)
	return false, errorResp.Message
}

func (p *PushplusNotifier) Test() (bool, string) {
	return p.Notify("Test message - 测试消息")
}
