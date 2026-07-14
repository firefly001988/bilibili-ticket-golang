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

func TestBuildGaiaSecureEnvironment(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://www.bilibili.com/")
	jar.SetCookies(u, []*http.Cookie{
		{Name: "DedeUserID", Value: "42", Path: "/", Domain: ".bilibili.com"},
		{Name: "buvid3", Value: "BUVID3-STABLE", Path: "/", Domain: ".bilibili.com"},
		{Name: "b_nut", Value: "123456", Path: "/", Domain: ".bilibili.com"},
		{Name: "b_lsid", Value: "LSID-STABLE", Path: "/", Domain: ".bilibili.com"},
		{Name: "buvid4", Value: "BUVID4-STABLE", Path: "/", Domain: ".bilibili.com"},
	})
	browser := utils.MobileFingerprintFromLegacy("mobile-ua", "0123456789abcdefghijklmnopqrstuv", "renderer-abcdefghijklmnopqrstuvwxyz-0123456789")
	client := &BiliClient{
		cookieJar: jar,
		fingerprint: &Fingerprint{
			Buvidfp: "fingerprint-id",
			Browser: browser,
		},
	}

	payload, err := client.buildGaiaSecureEnvironment(api.GaiaSecureFingerprintOptions{
		CollectAPI: "ticket-order",
		PageURL:    "https://show.bilibili.com/",
		SPMID:      "show.ticket",
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	assertJSONString(t, fields[exClimbCongLingFieldKeys["mid"]], "42")
	assertJSONString(t, fields[exClimbCongLingFieldKeys["buvid"]], "BUVID3-STABLE")
	assertJSONString(t, fields[exClimbCongLingFieldKeys["user_agent"]], "mobile-ua")
	assertJSONString(t, fields[exClimbCongLingFieldKeys["dt"]], "")
	assertJSONString(t, fields[exClimbCongLingFieldKeys["collect_api"]], "ticket-order")
	assertJSONString(t, fields[exClimbCongLingFieldKeys["hardwareConcurrency"]], "8")
	assertJSONString(t, fields[exClimbCongLingFieldKeys["screenResolution"]], "[1699,834]")
	assertJSONString(t, fields["lsid"], "LSID-STABLE")
	assertJSONString(t, fields["buvid4"], "BUVID4-STABLE")
	assertJSONString(t, fields["sdk_version"], "0.1.15")
	if _, exists := fields["user_agent"]; exists {
		t.Fatal("untranslated secure field leaked into payload")
	}
}
