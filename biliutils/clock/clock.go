package clock

import (
	"bilibili-ticket-golang/models/bili/api"
	"time"

	"github.com/beevik/ntp"
	"github.com/imroc/req/v3"
)

// GetBilibiliClockOffset queries Bilibili's live API to compute clock offset.
//
// The returned offset is (server_time - local_time) using a full-RTT estimate:
// the server's Microtime is interpreted as the server's wall clock at the
// moment it produced the response, and the request's elapsed time is
// subtracted from the local send-time to estimate that same moment locally.
func GetBilibiliClockOffset() (time.Duration, error) {
	now := time.Now()
	res, err := req.R().EnableTrace().Get("https://api.live.bilibili.com/xlive/open-interface/v1/rtc/getTimestamp")
	if err != nil {
		return 0, err
	}
	var r api.MainApiDataRoot[api.RTCTimestamp]
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
