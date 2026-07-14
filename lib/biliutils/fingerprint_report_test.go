package biliutils

import (
	"bilibili-ticket-golang/lib/models/bili/api"
	"bilibili-ticket-golang/lib/utils"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

func TestBuildGaiaFingerprintRequest(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://www.bilibili.com/")
	jar.SetCookies(u, []*http.Cookie{
		{Name: "DedeUserID", Value: "42", Path: "/", Domain: ".bilibili.com"},
		{Name: "buvid3", Value: "BUVID3-STABLE", Path: "/", Domain: ".bilibili.com"},
	})

	browser := utils.MobileFingerprintFromLegacy("mobile-ua", "0123456789abcdefghijklmnopqrstuv", "renderer-abcdefghijklmnopqrstuvwxyz-0123456789")
	client := &BiliClient{
		buvid:     "fallback-buvid",
		cookieJar: jar,
		fingerprint: &Fingerprint{
			Buvidfp: "fingerprint-id",
			Browser: browser,
		},
	}
	body, err := client.buildGaiaFingerprintRequest(api.GaiaFingerprintOptions{
		SPMPrefix: "show.ticket",
		PageURL:   "https://show.bilibili.com/",
	})
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]json.RawMessage
	if err = json.Unmarshal([]byte(body.Payload), &payload); err != nil {
		t.Fatalf("payload is not nested JSON: %v", err)
	}
	assertJSONString(t, payload[exClimbWuzhiFieldKeys["spm_id"]], "show.ticket.fp.risk")
	assertJSONString(t, payload[exClimbWuzhiFieldKeys["uuid"]], "BUVID3-STABLE")
	assertJSONString(t, payload[exClimbWuzhiFieldKeys["mid"]], "42")

	var message map[string]json.RawMessage
	if err = json.Unmarshal(payload[exClimbWuzhiFieldKeys["msg"]], &message); err != nil {
		t.Fatalf("message is not an object: %v", err)
	}
	assertJSONString(t, message[exClimbWuzhiFieldKeys["userAgent"]], "mobile-ua")
	assertJSONString(t, message[exClimbWuzhiFieldKeys["canvas"]], "cdefghijklmnopqrstuv")
	if _, exists := message["userAgent"]; exists {
		t.Fatal("untranslated field leaked into Gaia message")
	}
	if string(message[exClimbWuzhiFieldKeys["cookieEnabled"]]) != "1" {
		t.Fatalf("cookieEnabled was not converted to 1: %s", message[exClimbWuzhiFieldKeys["cookieEnabled"]])
	}
}

func TestBuildGaiaFingerprintRequestRejectsInvalidURL(t *testing.T) {
	client := &BiliClient{fingerprint: &Fingerprint{Browser: utils.GenerateRandomMobileFingerprint("ua")}}
	if _, err := client.buildGaiaFingerprintRequest(api.GaiaFingerprintOptions{PageURL: "://bad"}); err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func assertJSONString(t *testing.T, raw json.RawMessage, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode string %s: %v", raw, err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
