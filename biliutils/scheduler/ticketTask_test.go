package scheduler

import (
	"bilibili-ticket-golang/models/bili/api"
	"encoding/json"
	"testing"
)

// submitAction represents the action the submit loop should take after
// classifying a SubmitOrder response.
type submitAction int

const (
	actionRetry       submitAction = iota // keep retrying (goto SLEEP)
	actionSuccess                         // order succeeded → stop
	actionFail                            // terminal failure → stop
	actionPriceUpdate                     // price changed → update price, keep retrying
)

// classifySubmitResponse models the decision logic inside ticketFunc's submit loop.
// It takes the raw SubmitOrder return values and returns the action to take.
//
// This is extracted so we can unit-test all code paths without needing a real
// BiliClient / network.
func classifySubmitResponse(err error, code int, orderID int64) submitAction {
	if err != nil {
		// Network / HTTP / voucher error (but voucher is now auto-resolved
		// inside WrapRoundTripFunc, so this branch is reached only for
		// truly unrecoverable errors).
		return actionRetry
	}

	// Success: OrderId is non-zero and code is 0, 100048, or 100079.
	if (code == 0 || code == 100048 || code == 100079) && orderID != 0 {
		return actionSuccess
	}

	switch code {
	case 100034:
		// Price change — update price, keep retrying.
		return actionPriceUpdate
	case 100017:
		// Not for sale — terminal.
		return actionFail
	}

	// All other codes (including code=0 with OrderId=0, e.g. "Request blocked
	// by navigator rule") → silently retry.
	return actionRetry
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestClassifySubmitResponse_Success(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		code        int
		orderID     int64
		want        submitAction
		description string
	}{
		// ---- Success paths ----
		{
			name:    "normal success",
			code:    0,
			orderID: 123456,
			want:    actionSuccess,
		},
		{
			name:    "success with code 100048",
			code:    100048,
			orderID: 789012,
			want:    actionSuccess,
		},
		{
			name:    "success with code 100079",
			code:    100079,
			orderID: 1,
			want:    actionSuccess,
		},

		// ---- OrderId zero with success codes → retry (not real success) ----
		{
			name:    "code 0 but no OrderId → retry",
			code:    0,
			orderID: 0,
			want:    actionRetry,
		},

		// ---- Price change (100034) ----
		{
			name:    "price change code 100034",
			code:    100034,
			orderID: 0,
			want:    actionPriceUpdate,
		},

		// ---- Not for sale (100017) ----
		{
			name:    "not for sale code 100017",
			code:    100017,
			orderID: 0,
			want:    actionFail,
		},

		// ---- navigator rule block (code=0, data=null) ----
		{
			name:        "navigator rule block (code=0, data=null → OrderId=0)",
			code:        0,
			orderID:     0,
			want:        actionRetry,
			description: `{"code":0,"message":"Request blocked by navigator rule defaultBBR","data":null}`,
		},

		// ---- Unknown codes → retry ----
		{
			name:    "unknown error code -1",
			code:    -1,
			orderID: 0,
			want:    actionRetry,
		},
		{
			name:    "unknown error code 429",
			code:    429,
			orderID: 0,
			want:    actionRetry,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifySubmitResponse(tt.err, tt.code, tt.orderID)
			if got != tt.want {
				t.Errorf("classifySubmitResponse(err=%v, code=%d, orderID=%d) = %d, want %d  (%s)",
					tt.err, tt.code, tt.orderID, got, tt.want, tt.description)
			}
		})
	}
}

// TestNavigatorRuleBlock_JsonUnmarshal verifies the exact path through
// SubmitOrder when the API returns:
//
//	{"code":0, "message":"Request blocked by navigator rule defaultBBR", "data":null}
//
// Because data is null and Data is a value type (not a pointer),
// json.Unmarshal overwrites the pre-initialised PayMoney=-1 with the
// zero-value TicketOrderStruct (OrderId=0, PayMoney=0, ...).
func TestNavigatorRuleBlock_JsonUnmarshal(t *testing.T) {
	// Simulate the exact JSON the API returns.
	rawJSON := `{"code":0,"message":"Request blocked by navigator rule defaultBBR","data":null}`

	// Reproduce SubmitOrder's initialisation exactly.
	var apiResp api.ShowApiDataRoot[api.TicketOrderStruct]

	err := json.Unmarshal([]byte(rawJSON), &apiResp)
	if err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	code := apiResp.GetCode()
	msg := apiResp.GetMessage()
	orderID := apiResp.Data.OrderId
	payMoney := apiResp.Data.PayMoney

	// Assert the unmarshalled values.
	if code != 0 {
		t.Errorf("GetCode() = %d, want 0", code)
	}
	if msg != "Request blocked by navigator rule defaultBBR" {
		t.Errorf("GetMessage() = %q, want exact navigator message", msg)
	}
	if orderID != 0 {
		t.Errorf("Data.OrderId = %d, want 0 (null in JSON zeroes the struct)", orderID)
	}
	// Critical: PayMoney was -1 before unmarshal, but null overwrites to zero-value.
	if payMoney != 0 {
		t.Errorf("Data.PayMoney = %d, want 0 — json null overwrites pre-init -1 for value types", payMoney)
	}

	// Now feed into the decision function.
	got := classifySubmitResponse(nil, code, orderID)
	if got != actionRetry {
		t.Fatalf("classifySubmitResponse = %d, want actionRetry (%d)", got, actionRetry)
	}

	t.Logf("✅ navigator rule block: code=%d orderID=%d payMoney=%d → actionRetry", code, orderID, payMoney)
}
