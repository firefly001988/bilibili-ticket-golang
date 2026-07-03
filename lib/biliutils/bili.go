package biliutils

import (
	"bilibili-ticket-golang/lib/githubutils"
	"bilibili-ticket-golang/lib/global"
	"bilibili-ticket-golang/lib/models/bili/api"
	"bilibili-ticket-golang/lib/models/errors"
	"bilibili-ticket-golang/lib/utils"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bertold/req/v3"
)

const model = "SM-S9080"

// Fingerprint holds device fingerprint data for anti-detection.
type Fingerprint struct {
	BuvidLocal string
	Buvidfp    string
	Webglfp    string
	Canvasfp   string
}

type DeviceProfile struct {
	Buvid       string      `json:"buvid"`
	InfocUUID   string      `json:"infocUuid"`
	Fingerprint Fingerprint `json:"fingerprint"`
}

// CaptchaSolverFn is a function that solves a geetest captcha given gt+challenge.
// It returns the validate string on success.
type CaptchaSolverFn func(gt, challenge string) (validate string, err error)

// BiliClient is the main Bilibili API client with device impersonation.
type BiliClient struct {
	client       *req.Client
	appVersion   *api.BiliAppVersionStruct
	buvid        string
	infocUUID    string
	fingerprint  *Fingerprint
	wbi          atomic.Pointer[wbiKey]
	cookieJar    http.CookieJar
	saveCookies  atomic.Pointer[func()]
	refreshToken atomic.Pointer[string]
	solver       atomic.Pointer[CaptchaSolverFn]

	// clockOffset is the calibrated time offset (server − local).
	// Positive means local clock is behind the server. Set via SetClockOffset.
	clockOffset atomic.Int64 // stored as nanoseconds (int64)

	mu sync.RWMutex
}

// SetClockOffset stores a calibrated clock offset for use in request timestamps.
// offset = server_time − local_time. Positive means local is behind.
func (c *BiliClient) SetClockOffset(offset time.Duration) {
	c.clockOffset.Store(int64(offset))
}

// GetClockOffset returns the current calibrated clock offset.
func (c *BiliClient) GetClockOffset() time.Duration {
	return time.Duration(c.clockOffset.Load())
}

// Now returns the calibrated current time (local time + clock offset).
func (c *BiliClient) Now() time.Time {
	return time.Now().Add(time.Duration(c.clockOffset.Load()))
}

// NewBiliClient creates a new BiliClient with random device fingerprint.
func NewBiliClient() (*BiliClient, error) {
	return newBiliClient(nil, nil)
}

// NewBiliClientWithCookiejar creates a new BiliClient with a cookie jar and random device fingerprint.
//
// The client automatically:
//   - Generates a random BUVID (XU-format) and infoc UUID for device ID
//   - Generates random browser fingerprint (buvidfp, webglfp, canvasfp, buvid_local)
//   - Uses Android TLS fingerprint + Chrome impersonation for anti-detection
//   - Sets BiliDroid/mall WebView User-Agent per-request based on the target host
//   - Injects show.bilibili.com-specific cookies (_uuid, buvid, feSign, screenInfo, etc.)
//   - Handles voucher (x-bili-gaia-vvoucher) responses transparently
func NewBiliClientWithCookiejar(jar http.CookieJar) (*BiliClient, error) {
	return newBiliClient(jar, nil)
}

func NewBiliClientWithDeviceProfile(jar http.CookieJar, profile DeviceProfile) (*BiliClient, error) {
	return newBiliClient(jar, &profile)
}

func newBiliClient(jar http.CookieJar, profile *DeviceProfile) (*BiliClient, error) {
	ver, err := GetBilibiliAppVersion()
	if err != nil {
		return nil, err
	}

	// Generate device fingerprint
	buvid := utils.GenerateXUBUVID()
	infocUUID := utils.GenerateUUIDInfoc()
	fp := &Fingerprint{
		BuvidLocal: utils.GetFpLocal(buvid, model, ""),
		Buvidfp:    utils.CalculateFingerprintID(utils.GenerateRandomFingerprint()),
		Webglfp:    utils.RandomString("0123456789abcdef", 32),
		Canvasfp:   utils.RandomString("0123456789abcdef", 32),
	}
	if profile != nil {
		buvid, infocUUID = profile.Buvid, profile.InfocUUID
		profileFingerprint := profile.Fingerprint
		fp = &profileFingerprint
	}

	biliClient := &BiliClient{
		appVersion:  ver,
		buvid:       buvid,
		infocUUID:   infocUUID,
		fingerprint: fp,
		cookieJar:   jar,
	}
	emptyKey := &wbiKey{}
	biliClient.wbi.Store(emptyKey)
	emptySolver := CaptchaSolverFn(nil)
	biliClient.solver.Store(&emptySolver)

	// Per-instance req.Client to avoid leaking TLS fingerprint, common cookies,
	// and cookie jar across multiple BiliClient instances.
	httpClient := req.NewClient()
	httpClient.SetTLSFingerprintAndroid().ImpersonateChrome()
	httpClient.SetCommonCookies()
	if jar != nil {
		httpClient.SetCookieJar(jar)
	}
	biliClient.client = httpClient

	// Wrap round trip for per-request header injection
	httpClient.WrapRoundTripFunc(func(rt req.RoundTripper) req.RoundTripFunc {
		return func(req *req.Request) (resp *req.Response, err error) {
			// Build User-Agent for show.bilibili.com (会员购)
			var ua string
			// Filter requests by host and path to set appropriate User-Agent and headers.
			if req.URL.Host == "passport.bilibili.com" || (req.URL.Host == "show.bilibili.com" && (req.URL.Path == "/api/ticket/order/createstatus" || req.URL.Path == "/api/ticket/order/getPayParam")) {
				ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.7727.56 Safari/537.36"
			} else if req.URL.Host == "show.bilibili.com" {
				req.SetHeader("x-requested-with", "tv.danmaku.bili")
				ua = fmt.Sprintf(
					`Mozilla/5.0 (Linux; Android 12; %s; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/101.0.4951.61 Safari/537.36 BiliApp/%d mobi_app/android isNotchWindow/0 NotchHeight=24 mallVersion/%d mVersion/361 disable_rcmd/0 magent/BILI_H5_ANDROID_12_%s_%d`,
					model, biliClient.appVersion.Build, biliClient.appVersion.Build, biliClient.appVersion.Version, biliClient.appVersion.Build,
				)
				// Set show.bilibili.com specific cookies
				req.SetCookies(
					&http.Cookie{Name: "_uuid", Value: biliClient.infocUUID},
					&http.Cookie{Name: "buvid", Value: biliClient.buvid},
					&http.Cookie{Name: "buvid_fp", Value: biliClient.fingerprint.Buvidfp},
					&http.Cookie{Name: "fp_local", Value: biliClient.fingerprint.BuvidLocal},
					&http.Cookie{Name: "kfcFrom", Value: "newhomepage"},
					&http.Cookie{Name: "from", Value: "newhomepage"},
					&http.Cookie{Name: "kfcSource", Value: "bilibiliapp"},
					&http.Cookie{Name: "mSource", Value: "bilibiliapp"},
					&http.Cookie{Name: "feSign", Value: getFeSign(ua, biliClient.fingerprint.Canvasfp, biliClient.fingerprint.Webglfp)},
					&http.Cookie{Name: "screenInfo", Value: screenInfo},
					&http.Cookie{Name: "deviceFingerprint", Value: getFeSign(ua, biliClient.fingerprint.Canvasfp, biliClient.fingerprint.Webglfp)},
					&http.Cookie{Name: "browser_resolution", Value: fmt.Sprintf("%d-%d", 1699, 834)},
				)
			} else {
				// BiliDroid UA for other endpoints
				ua = fmt.Sprintf(
					`Mozilla/5.0 BiliDroid/%s (bbcallen@gmail.com) os/android model/%s mobi_app/android build/%d channel/bili innerVer/%d osVer/12 network/2`,
					biliClient.appVersion.Version, model, biliClient.appVersion.Build, biliClient.appVersion.Build,
				)
			}

			// Set common headers
			if req.Headers.Get("Referer") == "" {
				req.SetHeader("Referer", "https://www.bilibili.com/")
			}
			req.SetHeader("User-Agent", ua)
			req.SetHeader("local_buvid", biliClient.buvid)
			req.SetHeader("buvid", biliClient.buvid)
			req.SetHeader("fp_local", biliClient.fingerprint.BuvidLocal)
			req.SetHeader("fp_remote", biliClient.fingerprint.BuvidLocal)

			resp, err = rt.RoundTrip(req)

			// When a captcha solver is installed, the voucher is resolved
			// automatically and the request is retried once.
			if err == nil {
				voucher := resp.Header.Get("x-bili-gaia-vvoucher")
				if voucher == "" {
					var voucherData api.MainApiDataRoot[api.VoucherStruct]
					if unmarshalErr := resp.Unmarshal(&voucherData); unmarshalErr == nil {
						if voucherData.Code == -352 && voucherData.Data.Voucher != "" {
							voucher = voucherData.Data.Voucher
						}
					}
				}
				if voucher != "" {
					solverPtr := biliClient.solver.Load()
					if solverPtr != nil && *solverPtr != nil {
						// Auto-resolve voucher with captcha solver, then retry.
						resolved, resolveErr := biliClient.resolveVoucher(voucher)
						// Close the original voucher response body to avoid resource leak.
						resp.Body.Close()
						if resolveErr != nil {
							return nil, fmt.Errorf("voucher resolve failed: %w", resolveErr)
						}
						// Retry: add voucher to query params and cookie, then re-send.
						// Directly rewrite URL RawQuery to avoid nil QueryParams panic.
						q := req.RawRequest.URL.Query()
						q.Set("gaia_vtoken", resolved)
						req.RawRequest.URL.RawQuery = q.Encode()
						req.Cookies = append(req.Cookies, &http.Cookie{Name: "x-bili-gaia-vtoken", Value: resolved})
						return rt.RoundTrip(req)
					}
					_ = resp.Body.Close()
					return nil, errors.NewBilibiliAPIVoucherError(voucher)
				}
			}
			return resp, err
		}
	})

	return biliClient, nil
}

func (c *BiliClient) ExportDeviceProfile() DeviceProfile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return DeviceProfile{Buvid: c.buvid, InfocUUID: c.infocUUID, Fingerprint: *c.fingerprint}
}

// GetBilibiliAppVersion fetches the latest Bilibili Android app version info.
// This is used to construct realistic User-Agent headers.
func GetBilibiliAppVersion() (*api.BiliAppVersionStruct, error) {
	resp, err := req.SetTLSFingerprintChrome().ImpersonateChrome().R().Get("https://app.bilibili.com/x/v2/version?mobi_app=android")
	if err != nil {
		return nil, err
	}

	var data api.MainApiDataRoot[[]*api.BiliAppVersionStruct]
	err = resp.UnmarshalJson(&data)
	if err != nil {
		return nil, err
	}
	return data.Data[0], nil
}

func (c *BiliClient) GetBrowserUA() string {
	return fmt.Sprintf(
		`Mozilla/5.0 (Linux; Android 12; %s; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/101.0.4951.61 Safari/537.36 BiliApp/%d mobi_app/android isNotchWindow/0 NotchHeight=24 mallVersion/%d mVersion/312 disable_rcmd/0 magent/BILI_H5_ANDROID_12_%s_%d`,
		model, c.appVersion.Build, c.appVersion.Build, c.appVersion.Version, c.appVersion.Build,
	)
}

// GetAccountStatus returns the current login status and user info.
// Calls Bilibili's nav endpoint. If logged in, returns name and UID.
func (c *BiliClient) GetAccountStatus() (*api.GetLoginInfoStruct, error) {
	res, err := c.client.R().Get("https://api.bilibili.com/x/web-interface/nav")
	if err != nil {
		return nil, err
	}
	var r api.MainApiDataRoot[*api.GetLoginInfoStruct]
	err = res.Unmarshal(&r)
	if err != nil {
		return nil, err
	}
	return r.Data, nil
}

// GetBUVID returns the current BUVID.
func (c *BiliClient) GetBUVID() string {
	return c.buvid
}

// GetFingerprint returns the current device fingerprint.
func (c *BiliClient) GetFingerprint() Fingerprint {
	return *c.fingerprint
}

// GetInfocUUID returns the current infoc UUID.
func (c *BiliClient) GetInfocUUID() string {
	return c.infocUUID
}

// IsDebug returns whether debug mode is enabled.
// Both frontend and backend use this to control verbose logging.
func (c *BiliClient) IsDebug() bool {
	return global.Debug
}

// GetAppVersion returns the current Bilibili app version info.
func (c *BiliClient) GetAppVersion() *api.BiliAppVersionStruct {
	return c.appVersion
}

// SetCookieSaveCallback sets a callback that is invoked whenever PersistCookies is called.
// The callback should dump all cookies from the jar and write them to persistent storage.
func (c *BiliClient) SetCookieSaveCallback(cb func()) {
	c.saveCookies.Store(&cb)
}

// PersistCookies triggers an immediate save of all cookies to persistent storage.
// It must be called after SetCookieSaveCallback has been called.
func (c *BiliClient) PersistCookies() {
	cbPtr := c.saveCookies.Load()
	if cbPtr != nil && *cbPtr != nil {
		(*cbPtr)()
	}
}

// SetRefreshToken stores the Bilibili refresh_token for cookie refresh operations.
// Call this after QR login succeeds (the token is in the QR login response).
func (c *BiliClient) SetRefreshToken(token string) {
	c.refreshToken.Store(&token)
}

// GetRefreshToken returns the stored refresh_token.
func (c *BiliClient) GetRefreshToken() string {
	tokPtr := c.refreshToken.Load()
	if tokPtr == nil {
		return ""
	}
	return *tokPtr
}

// GetCookieJar returns the underlying cookie jar.
func (c *BiliClient) GetCookieJar() http.CookieJar {
	return c.cookieJar
}

// CheckForUpdate queries GitHub for the latest release and compares it with
// the running build's commit hash. Returns an UpdateInfo struct serializable to JSON.
func (c *BiliClient) CheckForUpdate() *githubutils.UpdateInfo {
	checker := githubutils.NewChecker(
		"firefly001988",
		"bilibili-ticket-golang",
		global.GitCommit,
	)
	info, err := checker.CheckForUpdate()
	if err != nil {
		return &githubutils.UpdateInfo{
			HasUpdate:      false,
			CurrentVersion: global.GitCommit,
		}
	}
	return info
}

// getBuvid34AndBnut fetches buvid3 and buvid4 cookies from Bilibili after login.
func (c *BiliClient) getBuvid34AndBnut() error {
	// First visit to get initial cookies
	c.client.R().Head("https://www.bilibili.com/")

	res, err := c.client.R().Get("https://api.bilibili.com/x/frontend/finger/spi")
	if err != nil {
		return err
	}
	var r api.MainApiDataRoot[api.GetBVUID34Struct]
	err = res.Unmarshal(&r)
	if err != nil {
		return err
	}

	if c.cookieJar != nil {
		u, _ := url.Parse("https://www.bilibili.com/")
		c.cookieJar.SetCookies(u, []*http.Cookie{
			{Name: "buvid3", Value: r.Data.BVUID3, Path: "/", Domain: "bilibili.com", MaxAge: 60 * 60 * 24 * 365},
			{Name: "buvid4", Value: r.Data.BVUID4, Path: "/", Domain: "bilibili.com", MaxAge: 60 * 60 * 24 * 365},
		})
	}
	return nil
}

// getUID extracts the UID from cookies for use in headers and logging.
func (c *BiliClient) getUID() string {
	if c.cookieJar == nil {
		return ""
	}
	bilibiliURL, _ := url.Parse("https://www.bilibili.com/")
	for _, cookie := range c.cookieJar.Cookies(bilibiliURL) {
		if cookie.Name == "DedeUserID" {
			return cookie.Value
		}
	}
	return ""
}

// FetchAvatar downloads the avatar image from the given URL (with Bilibili
// Referer to bypass hotlink protection) and returns a base64 data URI.
// Returns empty string if the fetch fails.
func (c *BiliClient) FetchAvatar(faceURL string) string {
	if faceURL == "" {
		return ""
	}

	req, err := http.NewRequest("GET", faceURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Referer", "https://www.bilibili.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	// Read up to 512KB
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil || len(body) == 0 {
		return ""
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}

	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(body)
}
