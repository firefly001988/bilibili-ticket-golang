package clock

import (
	"bilibili-ticket-golang/lib/models/bili/api"
	"time"

	"github.com/beevik/ntp"
	"github.com/bertold/req/v3"
)

// GetBilibiliClockOffset queries Bilibili's live API to compute clock offset.
//
// The returned offset is (server_time - local_time) using a full-RTT estimate:
// the server's Microtime is interpreted as the server's wall clock at the
// moment it produced the response, and the request's elapsed time is
// subtracted from the local send-time to estimate that same moment locally.
func GetBilibiliClockOffset() (time.Duration, error) {
	offset, err := getBilibiliClockOffsetNew()
	if err == nil {
		return offset, nil
	}
	return getBilibiliClockOffsetOld()
}

func getBilibiliClockOffsetNew() (time.Duration, error) {
	now := time.Now()
	res, err := req.R().EnableTrace().Get("https://show.bilibili.com/api/activity/index/home/timestamp")
	if err != nil {
		return 0, err
	}
	var r api.MainApiDataRoot[int64]
	if err = res.Unmarshal(&r); err != nil {
		return 0, err
	}
	t := res.TraceInfo()
	offset := time.UnixMilli(r.Data).Sub(now) + t.ResponseTime
	return offset, nil
}

func getBilibiliClockOffsetOld() (time.Duration, error) {
	now := time.Now()
	res, err := req.R().EnableTrace().Get("https://api.live.bilibili.com/xlive/open-interface/v1/rtc/getTimestamp")
	if err != nil {
		return 0, err
	}
	var r api.ShowApiDataRoot[api.RTCTimestamp]
	if err = res.Unmarshal(&r); err != nil {
		return 0, err
	}
	t := res.TraceInfo()
	// Estimate when the server produced its response (half-RTT before the
	// local received-time): server_time_at_send ≈ now - ResponseTime/2
	// For "server - local" we use the simple (server_time - now) form,
	// which corresponds to a full-RTT skew (local time lags server).
	// Adjust to use the request-start moment on the local side:
	//   server_time ≈ unixMilli(r.Data.Microtime)
	//   local_time_at_send ≈ now - ResponseTime
	// offset = server_time - local_time_at_send
	//        = unixMilli(r.Data.Microtime) - (now - ResponseTime)
	serverTime := time.UnixMilli(r.Data.Microtime)
	offset := serverTime.Sub(now) + t.ResponseTime
	return offset, nil
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
