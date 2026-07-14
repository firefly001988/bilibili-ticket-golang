package biliutils

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
)

func TestGetBuvid3(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	bilibiliURL, err := url.Parse("https://www.bilibili.com/")
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(bilibiliURL, []*http.Cookie{{
		Name:   "buvid3",
		Value:  "BUVID3-STABLE",
		Path:   "/",
		Domain: ".bilibili.com",
	}})

	client := &BiliClient{cookieJar: jar}
	if got := client.GetBuvid3(); got != "BUVID3-STABLE" {
		t.Fatalf("GetBuvid3() = %q, want %q", got, "BUVID3-STABLE")
	}
}

func TestGetBuvid3WithoutCookieJar(t *testing.T) {
	client := &BiliClient{}
	if got := client.GetBuvid3(); got != "" {
		t.Fatalf("GetBuvid3() = %q, want empty string", got)
	}
}
