package api

// BWSReservationInfoStruct represents the full response from
// GET /x/activity/bws/online/park/info
type BWSReservationInfoStruct struct {
	UserTicketInfo  map[string]BWSTicketDetailStruct   `json:"user_ticket_info"`
	ReserveList     map[string][]BWSActivityInfoStruct `json:"reserve_list"`
	UserReserveInfo map[string][]BWSReservedInfoStruct `json:"user_reserve_info"`
}

// BWSTicketDetailStruct is a single ticket entry keyed by date (e.g. "20250711").
type BWSTicketDetailStruct struct {
	Ticket     string `json:"ticket"`
	ScreenName string `json:"screen_name"`
	SkuName    string `json:"sku_name"`
}

// BWSActivityInfoStruct is a single activity within a day's reserve list.
type BWSActivityInfoStruct struct {
	ReserveID        int    `json:"reserve_id"`
	ActTitle         string `json:"act_title"`
	ReserveBeginTime int64  `json:"reserve_begin_time"`
	ActBeginTime     int64  `json:"act_begin_time"`
	State            int    `json:"state"`
	DescribeInfo     string `json:"describe_info"`
}

// BWSReservedInfoStruct is a single already-reserved activity entry.
type BWSReservedInfoStruct struct {
	ReserveID int `json:"reserve_id"`
}

// BWSMyReservationsStruct represents the response from
// GET /x/activity/bws/online/park/myreserve
type BWSMyReservationsStruct struct {
	ReserveList map[string][]BWSMyReservationItemStruct `json:"reserve_list"`
}

// BWSMyReservationItemStruct is a single item in the user's my-reservation list.
type BWSMyReservationItemStruct struct {
	ActTitle        string `json:"act_title"`
	ReserveNo       string `json:"reserve_no"`
	ActBeginTime    int64  `json:"act_begin_time"`
	ActEndTime      int64  `json:"act_end_time"`
	ReserveLocation string `json:"reserve_location"`
	IsChecked       int    `json:"is_checked"`
	OnlineState     int    `json:"online_state"`
}
