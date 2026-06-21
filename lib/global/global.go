package global

// ── Build info ──────────────────────────────────────────
var (
	GitCommit   = "Development"
	BuildTime   = "-1"
	LoggerLevel = "6"

	// Debug controls verbose debug output on both frontend and backend.
	// When the `debug` build tag is active, this is set to true in debug_on.go.
	Debug = false
)

// ── Default constants shared across packages ────────────

// DefaultIntervalMs is the default polling interval between submit attempts (ms).
const DefaultIntervalMs = 500

// DefaultRingCapacity is the default number of log entries kept per task in memory.
const DefaultRingCapacity = 1000

// FrontVersion is the bilibili mall frontend version string sent in API requests.
const FrontVersion = "134"

// DefaultTicketExpireDays is how many days from now a ticket expires if no API expiry is available.
const DefaultTicketExpireDays = 30

// MaxTokenRefreshCount is the number of submit attempts before refreshing the order token.
const MaxTokenRefreshCount = 61
