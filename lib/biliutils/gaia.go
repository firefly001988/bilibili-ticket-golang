package biliutils

import (
	"bilibili-ticket-golang/lib/models/bili/api"
	"bilibili-ticket-golang/lib/utils"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var exClimbWuzhiFieldKeys = map[string]string{
	"url":                       "03bf",
	"spm_id":                    "39c8",
	"target_url":                "34f1",
	"timestamp":                 "5062",
	"screenx":                   "d402",
	"screeny":                   "654a",
	"browser_resolution":        "6e7c",
	"ptype":                     "3064",
	"msg":                       "3c43",
	"abtest":                    "54ef",
	"refer_url":                 "8b94",
	"uuid":                      "df35",
	"mid":                       "920b",
	"buvid":                     "201c",
	"laboratory":                "5f45",
	"is_selfdef":                "db46",
	"addBehavior":               "6527",
	"audio":                     "d02f",
	"availableScreenResolution": "d61f",
	"b_nut_h":                   "3bf4",
	"buvid_fp":                  "737f",
	"canvas_novalid":            "e8ad",
	"canvas":                    "13ab",
	"colorDepth":                "5766",
	"cookieEnabled":             "807e",
	"cpuClass":                  "d52f",
	"deviceMemory":              "1c57",
	"fonts":                     "a658",
	"hardwareConcurrency":       "0bd0",
	"hasLiedBrowser":            "097b",
	"hasLiedLanguages":          "ed31",
	"hasLiedOs":                 "72bd",
	"hasLiedResolution":         "2673",
	"indexedDb":                 "7003",
	"language":                  "07a4",
	"localStorage":              "3b21",
	"lsid":                      "507f",
	"openDatabase":              "8a1c",
	"platform":                  "adca",
	"plugins":                   "80c9",
	"screenResolution":          "748e",
	"sessionStorage":            "75b8",
	"timezone":                  "6aa9",
	"timezoneOffset":            "fc9d",
	"touchSupport":              "52cd",
	"userAgent":                 "b8ce",
	"webdriver":                 "641c",
	"webglVendorAndRenderer":    "6bc5",
	"webgl_novalid":             "102a",
	"webgl_params":              "a3c1",
	"webgl_str":                 "bfe9",
}

var exClimbCongLingFieldKeys = map[string]string{
	"os_source":                 "6365",
	"mid":                       "699d",
	"buvid":                     "ebfb",
	"ip":                        "7160",
	"user_agent":                "99b0",
	"accept":                    "38d9",
	"accept_encoding":           "ea99",
	"accept_language":           "3449",
	"os_platform":               "d16f",
	"local_time":                "85a8",
	"webglVendorAndRenderer":    "4ddd",
	"screen_size_info":          "cc62",
	"window_size_info":          "f981",
	"addBehavior":               "2aed",
	"audio":                     "0012",
	"availableScreenResolution": "4351",
	"canvas":                    "96cc",
	"canva_novalid":             "2a7e",
	"colorDepth":                "d1e1",
	"cookieEnabled":             "c4a4",
	"cpuClass":                  "603d",
	"deviceMemory":              "11d0",
	"fonts":                     "97d8",
	"hardwareConcurrency":       "9187",
	"hasLiedBrowser":            "afb0",
	"hasLiedLanguages":          "0859",
	"hasLiedOs":                 "ef42",
	"hasLiedResolution":         "1474",
	"indexedDb":                 "e755",
	"language":                  "22bc",
	"localStorage":              "5bac",
	"openDatabase":              "ab18",
	"platform":                  "3664",
	"plugins":                   "78d3",
	"screenResolution":          "7717",
	"sessionStorage":            "d1a8",
	"timezone":                  "bca5",
	"timezoneOffset":            "c757",
	"touchSupport":              "a045",
	"userAgent":                 "b3eb",
	"webdriver":                 "cd52",
	"webgl_str":                 "e52e",
	"webgl_params":              "5824",
	"webgl_novalid":             "2120",
	"b_nut_h":                   "0137",
	"buvid_fp":                  "422d",
	"isid":                      "f477",
	"browser_build_version":     "a3dc",
	"notify_message_api":        "0fef",
	"dt":                        "8b9b",
	"collect_api":               "2b31",
	"nav_oscpu":                 "27c6",
	"nav_languages":             "4fab",
	"nav_productsub":            "900b",
	"eval_length":               "1b36",
	"spmid":                     "2233",
	"path":                      "2b3b",
}

// ReportGaiaAfterLogin refreshes BUVID cookies and submits both Gaia reports.
// Call it after a login flow has placed the account cookies in the client jar.
func (c *BiliClient) ReportGaiaAfterLogin(ctx context.Context, options api.GaiaPostLoginReportOptions) error {
	if err := c.getBuvid34AndBnut(); err != nil {
		return fmt.Errorf("ReportGaiaAfterLogin getBuvid34AndBnut: %w", err)
	}
	c.PersistCookies()

	if err := c.ExClimbWuzhi(ctx, options.ExClimbWuzhi); err != nil {
		return fmt.Errorf("ReportGaiaAfterLogin ExClimbWuzhi: %w", err)
	}
	if err := c.ExClimbCongLing(ctx, options.ExClimbCongLing); err != nil {
		return fmt.Errorf("ReportGaiaAfterLogin ExClimbCongLing: %w", err)
	}
	return nil
}

// ExClimbCongLing sends the encrypted secure-collection report. The simulated
// detection bitset is all-zero, matching the
// original WASM output with an empty dt value.
func (c *BiliClient) ExClimbCongLing(ctx context.Context, options api.GaiaSecureFingerprintOptions) error {
	if c.secureCookie("buvid3") == "" {
		return errors.New("ExClimbCongLing requires buvid3 cookie")
	}
	publicKey, err := c.ExGetAxe(ctx)
	if err != nil {
		return err
	}
	if publicKey.Deadline > 0 && c.Now().Unix() > publicKey.Deadline {
		return errors.New("ExClimbCongLing public key has expired")
	}

	environment, err := c.buildGaiaSecureEnvironment(options)
	if err != nil {
		return err
	}
	encryptedPayload, encryptedKey, err := utils.HybridEncryptPKCS1v15(environment, publicKey.PublicKey, rand.Reader)
	if err != nil {
		return fmt.Errorf("encrypt ExClimbCongLing payload: %w", err)
	}

	body := api.GaiaSecureReportRequest{
		Header: api.GaiaSecureReportHeader{
			EncodeType:     2,
			PayloadType:    4,
			EncodedAESKey:  encryptedKey,
			Timestamp:      c.Now().UnixMilli(),
			EncodedVersion: publicKey.Version,
		},
		EncryptPayload: encryptedPayload,
	}
	resp, err := c.client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Accept-Language", "zh-CN,zh;q=0.9").
		SetHeader("Content-Type", "application/json").
		SetBodyJsonMarshal(body).
		Post("https://api.bilibili.com/x/internal/gaia-gateway/ExClimbCongLing")
	if err != nil {
		return fmt.Errorf("request ExClimbCongLing: %w", err)
	}

	var result api.MainApiDataRoot[json.RawMessage]
	if err = resp.Unmarshal(&result); err != nil {
		return fmt.Errorf("decode ExClimbCongLing response: %w", err)
	}
	if err = result.CheckValid(); err != nil {
		return fmt.Errorf("ExClimbCongLing rejected: %w", err)
	}
	return nil
}

// ExGetAxe obtains the RSA public key used by ExClimbCongLing.
func (c *BiliClient) ExGetAxe(ctx context.Context) (api.GetAxeResponse, error) {
	resp, err := c.client.R().SetContext(ctx).Get("https://api.bilibili.com/x/internal/gaia-gateway/ExGetAxe")
	if err != nil {
		return api.GetAxeResponse{}, fmt.Errorf("request ExGetAxe: %w", err)
	}
	var result api.MainApiDataRoot[api.GetAxeResponse]
	if err = resp.Unmarshal(&result); err != nil {
		return api.GetAxeResponse{}, fmt.Errorf("decode ExGetAxe response: %w", err)
	}
	if err = result.CheckValid(); err != nil {
		return api.GetAxeResponse{}, fmt.Errorf("ExGetAxe rejected: %w", err)
	}
	return result.Data, nil
}

func (c *BiliClient) buildGaiaSecureEnvironment(options api.GaiaSecureFingerprintOptions) ([]byte, error) {
	c.mu.RLock()
	fp := *c.fingerprint
	c.mu.RUnlock()

	pageURL := options.PageURL
	if pageURL == "" {
		pageURL = "https://www.bilibili.com/"
	}
	if _, err := url.ParseRequestURI(pageURL); err != nil {
		return nil, fmt.Errorf("invalid ExClimbCongLing page URL: %w", err)
	}
	collectAPI := options.CollectAPI
	if collectAPI == "" {
		collectAPI = "manual"
	}
	browser := fp.Browser
	dpr := browser.DevicePixelRatio
	if dpr == 0 {
		dpr = 3
	}

	environment := map[string]any{
		"os_source":                 "h5",
		"mid":                       c.getUID(),
		"buvid":                     c.secureCookie("buvid3"),
		"user_agent":                browser.UserAgent,
		"accept":                    "application/json, text/plain, */*",
		"accept_encoding":           "gzip, deflate, br, zstd",
		"accept_language":           "zh-CN,zh;q=0.9",
		"os_platform":               browser.Platform,
		"local_time":                c.Now().UnixMilli(),
		"webglVendorAndRenderer":    browser.WebGLVendorAndRenderer,
		"screen_size_info":          fmt.Sprintf("%dx%dx%dx%dx%g", browser.ScreenResolution[1], browser.ScreenResolution[0], browser.ColorDepth, browser.ColorDepth, dpr),
		"window_size_info":          fmt.Sprintf("%dx%dx%dx%d", browser.ScreenResolution[0], browser.ScreenResolution[1], browser.ScreenResolution[0], browser.ScreenResolution[1]),
		"audio":                     browser.AudioFingerprint,
		"availableScreenResolution": browser.AvailableScreenResolution,
		"canvas":                    tail(browser.CanvasFingerprint, 20),
		"colorDepth":                browser.ColorDepth,
		"cookieEnabled":             boolNumber(browser.CookieEnabled),
		"cpuClass":                  browser.CPUClass,
		"deviceMemory":              browser.DeviceMemory,
		"fonts":                     browser.Fonts,
		"hardwareConcurrency":       browser.HardwareConcurrency,
		"hasLiedBrowser":            boolNumber(browser.HasLiedBrowser),
		"hasLiedLanguages":          boolNumber(browser.HasLiedLanguages),
		"hasLiedOs":                 boolNumber(browser.HasLiedOS),
		"hasLiedResolution":         boolNumber(browser.HasLiedResolution),
		"indexedDb":                 boolNumber(browser.IndexedDB),
		"language":                  browser.Language,
		"localStorage":              boolNumber(browser.LocalStorage),
		"openDatabase":              boolNumber(browser.OpenDatabase),
		"platform":                  browser.Platform,
		"plugins":                   browser.Plugins,
		"screenResolution":          browser.ScreenResolution,
		"sessionStorage":            boolNumber(browser.SessionStorage),
		"timezone":                  browser.Timezone,
		"timezoneOffset":            browser.TimezoneOffset,
		"touchSupport":              browser.TouchSupport,
		"userAgent":                 browser.UserAgent,
		"webdriver":                 boolNumber(browser.Webdriver),
		"webgl_str":                 tail(browser.WebGLRenderer, 50),
		"webgl_params":              browser.WebGLParams,
		"b_nut_h":                   c.secureCookie("b_nut"),
		"buvid_fp":                  fp.Buvidfp,
		"lsid":                      c.secureCookie("b_lsid"),
		"buvid4":                    c.secureCookie("buvid4"),
		"sdk_version":               "0.1.15",
		"browser_build_version":     strings.TrimPrefix(browser.UserAgent, "Mozilla/"),
		"notify_message_api":        "default",
		"dt":                        "",
		"collect_api":               collectAPI,
		"nav_oscpu":                 "Linux aarch64",
		"nav_languages":             []string{browser.Language, "zh"},
		"nav_productsub":            "20030107",
		"eval_length":               33,
		"spmid":                     options.SPMID,
		"path":                      pageURL,
	}
	translated := make(map[string]string, len(environment))
	for key, value := range environment {
		if shortKey, ok := exClimbCongLingFieldKeys[key]; ok {
			key = shortKey
		}
		translated[key] = stringifyGaiaSecureValue(value)
	}
	data, err := json.Marshal(translated)
	if err != nil {
		return nil, fmt.Errorf("encode ExClimbCongLing environment: %w", err)
	}
	return data, nil
}

// The browser collector normalizes every environment value to a string before
// handing it to encrypt_data. Arrays and nested values are compact JSON strings.
func stringifyGaiaSecureValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	switch value.(type) {
	case [2]int, [3]int, []string, []int, []float64, [][]string, map[string]any:
		encoded, err := json.Marshal(value)
		if err == nil {
			return string(encoded)
		}
	}
	return fmt.Sprint(value)
}

func (c *BiliClient) secureCookie(name string) string {
	if c.cookieJar == nil {
		return ""
	}
	bilibiliURL, _ := url.Parse("https://www.bilibili.com/")
	for _, cookie := range c.cookieJar.Cookies(bilibiliURL) {
		if strings.EqualFold(cookie.Name, name) {
			return cookie.Value
		}
	}
	return ""
}

// ExClimbWuzhi submits the client's stable synthetic browser profile to Gaia.
// It intentionally remains explicit instead of running during client
// construction, because reporting is a network side effect.
func (c *BiliClient) ExClimbWuzhi(ctx context.Context, options api.GaiaFingerprintOptions) error {
	body, err := c.buildGaiaFingerprintRequest(options)
	if err != nil {
		return err
	}

	resp, err := c.client.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json;charset=UTF-8").
		SetBodyJsonMarshal(body).
		Post("https://api.bilibili.com/x/internal/gaia-gateway/ExClimbWuzhi")
	if err != nil {
		return fmt.Errorf("request ExClimbWuzhi: %w", err)
	}

	var result api.MainApiDataRoot[json.RawMessage]
	if err = resp.Unmarshal(&result); err != nil {
		return fmt.Errorf("decode ExClimbWuzhi response: %w", err)
	}
	if err = result.CheckValid(); err != nil {
		return fmt.Errorf("ExClimbWuzhi rejected: %w", err)
	}
	return nil
}

func (c *BiliClient) buildGaiaFingerprintRequest(options api.GaiaFingerprintOptions) (api.GaiaFingerprintRequest, error) {
	c.mu.RLock()
	fp := *c.fingerprint
	c.mu.RUnlock()

	pageURL := options.PageURL
	if pageURL == "" {
		pageURL = "https://www.bilibili.com/"
	}
	if _, err := url.ParseRequestURI(pageURL); err != nil {
		return api.GaiaFingerprintRequest{}, fmt.Errorf("invalid ExClimbWuzhi page URL: %w", err)
	}

	browser := fp.Browser
	message := gaiaFingerprintMessage(browser, fp.Buvidfp)
	inner := map[string]any{
		exClimbWuzhiFieldKeys["spm_id"]:             options.SPMPrefix + ".fp.risk",
		exClimbWuzhiFieldKeys["url"]:                pageURL,
		exClimbWuzhiFieldKeys["timestamp"]:          c.Now().UnixMilli(),
		exClimbWuzhiFieldKeys["browser_resolution"]: fmt.Sprintf("%dx%d", browser.ScreenResolution[0], browser.ScreenResolution[1]),
		exClimbWuzhiFieldKeys["uuid"]:               c.gaiaUUID(),
		exClimbWuzhiFieldKeys["mid"]:                c.getUID(),
		exClimbWuzhiFieldKeys["msg"]:                translateExClimbWuzhiFields(message),
	}

	payload, err := json.Marshal(inner)
	if err != nil {
		return api.GaiaFingerprintRequest{}, fmt.Errorf("encode ExClimbWuzhi payload: %w", err)
	}
	return api.GaiaFingerprintRequest{Payload: string(payload)}, nil
}

func gaiaFingerprintMessage(fp utils.FingerprintData, buvidFP string) map[string]any {
	return map[string]any{
		"audio":                     fp.AudioFingerprint,
		"availableScreenResolution": fp.AvailableScreenResolution,
		"buvid_fp":                  buvidFP,
		"canvas":                    tail(fp.CanvasFingerprint, 20),
		"colorDepth":                fp.ColorDepth,
		"cookieEnabled":             boolNumber(fp.CookieEnabled),
		"cpuClass":                  fp.CPUClass,
		"deviceMemory":              fp.DeviceMemory,
		"fonts":                     fp.Fonts,
		"hardwareConcurrency":       fp.HardwareConcurrency,
		"hasLiedBrowser":            boolNumber(fp.HasLiedBrowser),
		"hasLiedLanguages":          boolNumber(fp.HasLiedLanguages),
		"hasLiedOs":                 boolNumber(fp.HasLiedOS),
		"hasLiedResolution":         boolNumber(fp.HasLiedResolution),
		"indexedDb":                 boolNumber(fp.IndexedDB),
		"language":                  fp.Language,
		"localStorage":              boolNumber(fp.LocalStorage),
		"openDatabase":              boolNumber(fp.OpenDatabase),
		"platform":                  fp.Platform,
		"plugins":                   fp.Plugins,
		"screenResolution":          fp.ScreenResolution,
		"sessionStorage":            boolNumber(fp.SessionStorage),
		"timezone":                  fp.Timezone,
		"timezoneOffset":            fp.TimezoneOffset,
		"touchSupport":              fp.TouchSupport,
		"userAgent":                 fp.UserAgent,
		"webdriver":                 boolNumber(fp.Webdriver),
		"webglVendorAndRenderer":    fp.WebGLVendorAndRenderer,
		"webgl_params":              fp.WebGLParams,
		"webgl_str":                 tail(fp.WebGLRenderer, 50),
	}
}

func translateExClimbWuzhiFields(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		if shortKey, ok := exClimbWuzhiFieldKeys[key]; ok {
			output[shortKey] = value
		}
	}
	return output
}

func boolNumber(value bool) int {
	if value {
		return 1
	}
	return 0
}

func tail(value string, length int) string {
	if len(value) <= length {
		return value
	}
	return value[len(value)-length:]
}

func (c *BiliClient) gaiaUUID() string {
	if c.cookieJar != nil {
		bilibiliURL, _ := url.Parse("https://www.bilibili.com/")
		cookies := c.cookieJar.Cookies(bilibiliURL)
		for _, name := range []string{"buvid3", "_uuid"} {
			for _, cookie := range cookies {
				if cookie.Name == name {
					return cookie.Value
				}
			}
		}
	}
	return c.buvid
}
