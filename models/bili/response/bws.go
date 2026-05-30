package _return

// BWSActivity represents a single BWS activity parsed from the API response.
type BWSActivity struct {
	ReserveID        int
	ActTitle         string
	ReserveBeginTime int64
	ActBeginTime     int64
	State            int
	DescribeInfo     string
	ReserveDate      string // the date key this activity belongs to (e.g. "20250711")
}

// BWSTicketInfo represents a user's ticket for a specific date.
type BWSTicketInfo struct {
	Date       string
	Ticket     string
	ScreenName string
	SkuName    string
}

// BWSReservationData aggregates all parsed reservation information
// needed by the BWS task to locate the correct ticket for an activity.
type BWSReservationData struct {
	// TicketMapping maps date (e.g. "20250711") → ticket_no
	TicketMapping map[string]string
	// TicketInfo maps date → full ticket info (for display)
	TicketInfo map[string]BWSTicketInfo
	// ActivityMapping maps activity_id → BWSActivity (for quick lookup)
	ActivityMapping map[int]*BWSActivity
	// ReservedIDs is the set of activity IDs the user has already reserved
	ReservedIDs map[int]bool
}
