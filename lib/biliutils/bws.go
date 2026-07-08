package biliutils

import (
	"bilibili-ticket-golang/lib/models/bili/api"
	r "bilibili-ticket-golang/lib/models/bili/response"
	"fmt"
	"strconv"
)

const year = "202601"

// ── BWS (Bilibili World) 预约抢票 API ──────────────────────────────────────

// Inspired from https://github.com/Starsbon/bws_ticket
// May not suit for BW2026, update reqired if BW2026 reservation API differs significantly from BW2025's.

// GetBWSReservationInfo fetches all reservation information for the given dates.
//
// reserveDates is comma-separated, e.g. "20250711,20250712,20250713".
// reserveType specifies the type of reservation to fetch. `0` for activities, `1` for goods.
//
// Returns a parsed BWSReservationData containing ticket mapping, activity
// mapping, and already-reserved activity IDs for convenient access.
func (c *BiliClient) GetBWSReservationInfo(reserveDates string, reserveType int) (*r.BWSReservationData, error) {
	resp, err := c.client.R().
		SetQueryString(c.SignAppParams(map[string]any{
			"reserve_date": reserveDates,
			"year":         year,
			"reserve_type": reserveType,
		}).Encode()).
		Get("https://api.bilibili.com/x/activity/bws/online/park/reserve/info")
	if err != nil {
		return nil, fmt.Errorf("BWS info request failed: %w", err)
	}
	var apiResp api.MainApiDataRoot[api.BWSReservationInfoStruct]
	if err = resp.Unmarshal(&apiResp); err != nil {
		return nil, fmt.Errorf("BWS info unmarshal failed: %w", err)
	}
	if err = apiResp.CheckValid(); err != nil {
		return nil, err
	}

	// Build parsed reservation data
	data := &r.BWSReservationData{
		TicketMapping:   make(map[string]string),
		TicketInfo:      make(map[string]r.BWSTicketInfo),
		ActivityMapping: make(map[int]*r.BWSActivity),
		ReservedIDs:     make(map[int]bool),
	}

	for date, ticket := range apiResp.Data.UserTicketInfo {
		data.TicketMapping[date] = ticket.Ticket
		data.TicketInfo[date] = r.BWSTicketInfo{
			Date:       date,
			Ticket:     ticket.Ticket,
			ScreenName: ticket.ScreenName,
			SkuName:    ticket.SkuName,
		}
	}

	for date, activities := range apiResp.Data.ReserveList {
		for _, act := range activities {
			bwsAct := &r.BWSActivity{
				ReserveID:        act.ReserveID,
				ActTitle:         act.ActTitle,
				ReserveBeginTime: act.ReserveBeginTime,
				ActBeginTime:     act.ActBeginTime,
				State:            act.State,
				DescribeInfo:     act.DescribeInfo,
				ReserveDate:      date,
			}
			data.ActivityMapping[act.ReserveID] = bwsAct
		}
	}

	return data, nil
}

// GetBWSMyReservations fetches the user's current BWS reservations.
func (c *BiliClient) GetBWSMyReservations() (*api.BWSMyReservationsStruct, error) {
	resp, err := c.client.R().
		SetQueryString(c.SignAppParams(map[string]any{
			"year": year,
		}).Encode()).
		Get("https://api.bilibili.com/x/activity/bws/online/park/myreserve")
	if err != nil {
		return nil, fmt.Errorf("BWS myreserve request failed: %w", err)
	}

	var apiResp api.MainApiDataRoot[api.BWSMyReservationsStruct]
	if err = resp.Unmarshal(&apiResp); err != nil {
		return nil, fmt.Errorf("BWS myreserve unmarshal failed: %w", err)
	}
	if err = apiResp.CheckValid(); err != nil {
		return nil, err
	}
	return &apiResp.Data, nil
}

// MakeBWSReservation submits a reservation request for a specific activity.
//
// Parameters:
//   - ticketNo: the electronic ticket number for the corresponding date
//   - reservationID: the activity's reserve_id
//
// Returns the API response code, message (even when code != 0), and a Go error
// only for transport/unmarshal failures.
func (c *BiliClient) MakeBWSReservation(ticketNo string, reservationID int) (code int, message string, err error) {
	csrf := c.getCSRFFromCookie()
	if csrf == "" {
		return -1, "missing csrf token", fmt.Errorf("BWS reservation: csrf token (bili_jct) not found in cookie jar")
	}

	form := map[string]string{
		"ticket_no":        ticketNo,
		"csrf":             csrf,
		"inter_reserve_id": strconv.Itoa(reservationID),
		"year":             year,
	}

	resp, reqErr := c.client.R().
		SetFormData(form).
		Post("https://api.bilibili.com/x/activity/bws/online/park/reserve/do")
	if reqErr != nil {
		return -1, "", fmt.Errorf("BWS reservation request failed: %w", reqErr)
	}

	var apiResp api.MainApiDataRoot[struct{}]
	if unmarshalErr := resp.Unmarshal(&apiResp); unmarshalErr != nil {
		return -1, "", fmt.Errorf("BWS reservation unmarshal failed: %w", unmarshalErr)
	}

	return apiResp.Code, apiResp.Message, nil
}

// BindBWSTicket binds real-name identity to an electronic ticket.
//
// Required before making reservations if the account hasn't bound yet
// (the /reserve/info endpoint returns code 75638 when unbound).
//
// Parameters:
//   - bid: activity bid, e.g. 202501 for BWS 2025
//   - idType: 0=身份证, 1=护照, 2=港澳通行证, 3=台湾通行证
//   - personalID: the ID number
//   - ticketNo: the 4-digit electronic ticket number
//   - userName: the real name on the ID
//
// Returns the API response code, message, and any transport/unmarshal error.
func (c *BiliClient) BindBWSTicket(bid int, idType int, personalID string, ticketNo string, userName string) (code int, message string, err error) {
	csrf := c.getCSRFFromCookie()
	if csrf == "" {
		return -1, "missing csrf token", fmt.Errorf("BWS bind: csrf token (bili_jct) not found in cookie jar")
	}

	form := map[string]string{
		"bid":         strconv.Itoa(bid),
		"csrf":        csrf,
		"id_type":     strconv.Itoa(idType),
		"personal_id": personalID,
		"ticket_no":   ticketNo,
		"user_name":   userName,
		"year":        year,
	}

	resp, reqErr := c.client.R().
		SetFormData(form).
		Post("https://api.bilibili.com/x/activity/bws/online/park/ticket/bind")
	if reqErr != nil {
		return -1, "", fmt.Errorf("BWS bind request failed: %w", reqErr)
	}

	var apiResp api.MainApiDataRoot[struct{}]
	if unmarshalErr := resp.Unmarshal(&apiResp); unmarshalErr != nil {
		return -1, "", fmt.Errorf("BWS bind unmarshal failed: %w", unmarshalErr)
	}

	return apiResp.Code, apiResp.Message, nil
}

// CheckBWSBindStatus checks the user's BWS bind status.
//
// Returns a boolean indicating if the account is bound, and any transport/unmarshal error.
func (c *BiliClient) CheckBWSBindStatus() (bool, error) {
	resp, err := c.client.R().
		SetQueryString(c.SignAppParams(map[string]any{
			"csrf": c.getCSRFFromCookie(),
			"year": year,
		}).Encode()).
		Get("https://api.bilibili.com/x/activity/bws/online/park/ticket/check")
	if err != nil {
		return false, fmt.Errorf("BWS bind check request failed: %w", err)
	}
	var apiResp api.MainApiDataRoot[api.BWSBindStatusStruct]
	if err = resp.Unmarshal(&apiResp); err != nil {
		return false, fmt.Errorf("BWS bind check unmarshal failed: %w", err)
	}
	if err = apiResp.CheckValid(); err != nil {
		return false, err
	}
	return apiResp.Data.IsBind, nil
}
