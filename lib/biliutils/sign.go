package biliutils

import (
	"bilibili-ticket-golang/lib/models/bili/api"
	"bilibili-ticket-golang/lib/utils"
	hashs "bilibili-ticket-golang/lib/utils/hashs"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// mixinKeyEncTab is the WBI key permutation table used by Bilibili.
// It defines how to rearrange characters from the combined img_url + sub_url
// to produce the 32-byte mixin key.
var mixinKeyEncTab = [64]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
	37, 48, 7, 16, 24, 55, 40, 61, 26, 17, 0, 1, 60, 51, 30, 4,
	22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36, 20, 34, 44, 52,
}

const (
	// appKey and appSec are the built-in Bilibili Android APP credentials for signature.
	appKey = "1d8b6e7d45233436"
	appSec = "560c52ccd288fed045859ed18bffd973"

	// screenInfo simulates a mobile device screen: width*height*colorDepth.
	screenInfo = "1699*834*24"
)

// wbiKey stores the WBI mixin key and its expiration time.
// WBI keys rotate daily at midnight (CST).
type wbiKey struct {
	mixin  string
	expire time.Time
}

// isExpired checks whether the WBI key has passed its expiration.
func (w *wbiKey) isExpired(now time.Time) bool {
	return now.After(w.expire)
}

// refreshWbiToken fetches new WBI image URLs from Bilibili, computes the mixin key,
// and updates the client's wbiKey. The key expires at midnight CST or 1 hour
// from now, whichever is later.
func (c *BiliClient) refreshWbiToken() error {
	resp, err := c.client.R().Get("https://api.bilibili.com/x/web-interface/nav")
	if err != nil {
		return err
	}
	var navResp api.MainApiDataRoot[api.WbiStruct]
	if err = resp.Unmarshal(&navResp); err != nil {
		return err
	}

	// Combine and permute file names from img_url and sub_url
	combinedNames := utils.GetFileNameWithoutExt(navResp.Data.WbiImg.ImgUrl) +
		utils.GetFileNameWithoutExt(navResp.Data.WbiImg.SubUrl)
	if len(combinedNames) < len(mixinKeyEncTab) {
		return fmt.Errorf("WBI key: combined names too short (got %d, need %d)", len(combinedNames), len(mixinKeyEncTab))
	}
	var builder strings.Builder
	for _, index := range mixinKeyEncTab {
		builder.WriteByte(combinedNames[index])
	}
	key := builder.String()[:32]

	now := c.now()
	expired := now.Add(1 * time.Hour)
	tomorrow := now.Add(24 * time.Hour).Truncate(24 * time.Hour)
	if utils.IsNextDayInCST(now, expired) {
		expired = tomorrow
	}

	c.wbi.Store(&wbiKey{
		mixin:  key,
		expire: expired,
	})
	return nil
}

// SignWithWbi signs URL query parameters using Bilibili's WBI algorithm.
//
// It adds w_rid (MD5(query + mixin_key)) and wts (current Unix timestamp)
// parameters to the URL. If the WBI key is expired or forceUpdate is true,
// a new key is fetched automatically.
func (c *BiliClient) SignWithWbi(forceUpdate bool, targetURL *url.URL) error {
	now := c.now()
	keyPtr := c.wbi.Load()
	if keyPtr == nil || keyPtr.isExpired(now) || forceUpdate {
		if err := c.refreshWbiToken(); err != nil {
			return err
		}
		keyPtr = c.wbi.Load()
		if keyPtr == nil {
			return fmt.Errorf("WBI key unavailable after refresh")
		}
	}
	values := targetURL.Query()
	values.Del("w_rid")
	values.Set("wts", fmt.Sprintf("%d", now.Unix()))
	wbiHash := md5.Sum([]byte(values.Encode() + keyPtr.mixin))
	values.Set("w_rid", hex.EncodeToString(wbiHash[:]))
	targetURL.RawQuery = values.Encode()
	return nil
}

// SignAppParams signs a parameter map using Bilibili's Android APP signature algorithm.
//
// It adds appkey and ts parameters, then computes sign = MD5(sorted_params + appSec).
// Used for endpoints requiring APP-level authentication.
func (c *BiliClient) SignAppParams(params map[string]any) url.Values {
	values := url.Values{}
	for key, val := range params {
		values.Set(key, fmt.Sprint(val))
	}
	values.Del("sign")
	values.Del("appkey")
	values.Del("ts")
	values.Set("appkey", appKey)
	values.Set("ts", strconv.FormatInt(c.now().Unix(), 10))
	sign := md5.Sum([]byte(values.Encode() + appSec))
	values.Set("sign", hex.EncodeToString(sign[:]))
	return values
}

// getIdentifyCookieSign generates the "identify" cookie value using APP-level signing.
func (c *BiliClient) getIdentifyCookieSign() http.Cookie {
	query := c.SignAppParams(map[string]any{
		"ts": c.now().Unix(),
	})
	return http.Cookie{
		Name:  "identify",
		Value: query.Encode(),
	}
}

// getFeSign computes the feSign cookie value using MurmurHash3 x64 128-bit.
//
// The hash input is: canvasFp ~ webglFp ~ screenInfo ~ userAgent
// with seed=31. The result is formatted as two 16-digit hex numbers.
func getFeSign(userAgent, canvasFp, webglFp string) string {
	h1, h2 := hashs.MurmurX64Hash128(
		fmt.Sprintf("%s~%s~%s~%s", canvasFp, webglFp, screenInfo, userAgent), 31,
	)
	return fmt.Sprintf("%016x%016x", h1, h2)
}
