package cluster_service

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cmd/gui/store/cookiejar"
	"bilibili-ticket-golang/lib/biliutils"
)

// accountClient creates a BiliClient from stored credentials.
func accountClient(account domain.Account) (*biliutils.BiliClient, *cookiejar.Jar, error) {
	jar := cookiejar.New(nil)
	for _, saved := range account.Credentials.CookieJar {
		host := strings.TrimPrefix(saved.Domain, ".")
		if host == "" {
			host = "www.bilibili.com"
		}
		u, _ := url.Parse("https://" + host + "/")
		cookie := &http.Cookie{Name: saved.Name, Value: saved.Value, Domain: saved.Domain, Path: saved.Path, Secure: saved.Secure, HttpOnly: saved.HTTPOnly}
		if saved.Expires > 0 {
			cookie.Expires = time.Unix(saved.Expires, 0)
		}
		jar.SetCookies(u, []*http.Cookie{cookie})
	}
	cookies := make([]*http.Cookie, 0, len(account.Credentials.Cookies))
	for name, value := range account.Credentials.Cookies {
		cookies = append(cookies, &http.Cookie{Name: name, Value: value, Path: "/"})
	}
	for _, raw := range []string{"https://www.bilibili.com/", "https://show.bilibili.com/", "https://passport.bilibili.com/"} {
		u, _ := url.Parse(raw)
		jar.SetCookies(u, cookies)
	}
	var client *biliutils.BiliClient
	var err error
	if len(account.Credentials.DeviceProfile) > 0 {
		var profile biliutils.DeviceProfile
		if decodeErr := json.Unmarshal(account.Credentials.DeviceProfile, &profile); decodeErr != nil {
			return nil, nil, decodeErr
		}
		client, err = biliutils.NewBiliClientWithDeviceProfile(jar, profile)
	} else {
		client, err = biliutils.NewBiliClientWithCookiejar(jar)
	}
	if err != nil {
		return nil, nil, err
	}
	client.SetRefreshToken(account.Credentials.RefreshToken)
	return client, jar, nil
}

// credentialsFrom extracts the current credentials from a BiliClient and
// cookie jar, merging with previous credentials to preserve Version etc.
func credentialsFrom(client *biliutils.BiliClient, jar *cookiejar.Jar, previous domain.Credentials) domain.Credentials {
	values := make(map[string]string)
	full := make([]domain.HTTPCookie, 0)
	for _, entry := range jar.AllEntries() {
		values[entry.Name] = entry.Value
		full = append(full, domain.HTTPCookie{Name: entry.Name, Value: entry.Value, Domain: entry.Domain, Path: entry.Path, Secure: entry.Secure, HTTPOnly: entry.HttpOnly, Expires: entry.Expires})
	}
	previous.CookieJar = full
	if len(values) > 0 {
		previous.Cookies = values
	}
	previous.RefreshToken = client.GetRefreshToken()
	if len(previous.DeviceProfile) == 0 {
		previous.DeviceProfile, _ = json.Marshal(client.ExportDeviceProfile())
	}
	return previous
}
