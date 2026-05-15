package clock

import (
	"bilibili-ticket-golang/models/bili/api"
	"time"

	"github.com/beevik/ntp"
	"github.com/imroc/req/v3"
)

// GetBilibiliClockOffset queries Bilibili's live API to compute clock offset.
func GetBilibiliClockOffset() (time.Duration, error) {
	now := time.Now()
	res, err := req.R().EnableTrace().Get("https://api.live.bilibili.com/xlive/open-interface/v1/rtc/getTimestamp")
	if err != nil {
		return 0, err
	}
	var r api.MainApiDataRoot[api.RTCTimestamp]
	err = res.Unmarshal(&r)
	if err != nil {
		return 0, err
	}
	t := res.TraceInfo()
	networkOffset := t.FirstResponseTime + t.ResponseTime
	return time.UnixMilli(r.Data.Microtime).Add(-networkOffset).Sub(now), nil
}

// GetNTPClockOffset queries an NTP server and returns the clock offset.
// Recommended: "ntp.aliyun.com"
func GetNTPClockOffset(ntpServerAddr string) (time.Duration, error) {
	q, err := ntp.Query(ntpServerAddr)
	if err != nil {
		return 0, err
	}
	return q.ClockOffset, nil
}
