package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Phase string

const (
	PhasePunctual Phase = "punctual"
	PhaseReflow   Phase = "reflow"
)

type ResourceRole string

const (
	RolePrimary ResourceRole = "primary"
	RoleStandby ResourceRole = "standby"
)

type AttemptState string

const (
	AttemptQueued    AttemptState = "queued"
	AttemptWaiting   AttemptState = "waiting"
	AttemptRunning   AttemptState = "running"
	AttemptStopping  AttemptState = "stopping"
	AttemptStopped   AttemptState = "stopped"
	AttemptSucceeded AttemptState = "succeeded"
	AttemptFailed    AttemptState = "failed"
)

func (s AttemptState) Terminal() bool {
	return s == AttemptStopped || s == AttemptSucceeded || s == AttemptFailed
}

type StartMode string

const (
	StartImmediate StartMode = "immediate"
	StartScheduled StartMode = "scheduled"
)

type FailureReason string

const (
	FailureNone          FailureReason = ""
	FailureDeadline      FailureReason = "deadline"
	FailureStopped       FailureReason = "stopped"
	FailureCookieInvalid FailureReason = "cookie_invalid"
	FailureHTTP412       FailureReason = "http_412"
	FailureCaptcha       FailureReason = "captcha"
	FailureAccountRisk   FailureReason = "account_risk"
	FailureWorkerLost    FailureReason = "worker_lost"
	FailureUnrecoverable FailureReason = "unrecoverable"
	FailureInternal      FailureReason = "internal"
)

type Buyer struct {
	LogicalID string `json:"logicalId"`
	BuyerID   int64  `json:"buyerId"`
	Name      string `json:"name"`
	Tel       string `json:"tel,omitempty"`
	IDCard    string `json:"idCard,omitempty"`
	Type      int    `json:"type"`
}

type BuyerDayKey struct {
	BuyerID  string `json:"buyerId"`
	EventDay string `json:"eventDay"`
}

func (k BuyerDayKey) String() string { return k.BuyerID + "@" + k.EventDay }

type TaskGroup struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	AccountIDs            []string  `json:"accountIds,omitempty"`
	PrimaryWorkerIDs      []string  `json:"primaryWorkerIds,omitempty"`
	StandbyWorkerIDs      []string  `json:"standbyWorkerIds,omitempty"`
	PaymentTimeoutMinutes int       `json:"paymentTimeoutMinutes,omitempty"`
	WaveDurationMinutes   int       `json:"waveDurationMinutes,omitempty"`
	MaxWaves              int       `json:"maxWaves,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
}

type CapacitySource string

const (
	CapacityAPI      CapacitySource = "api"
	CapacityOverride CapacitySource = "override"
	CapacityDefault  CapacitySource = "default"
)

type MacroTask struct {
	ID                string         `json:"id"`
	TaskGroupID       string         `json:"taskGroupId"`
	ProjectID         int64          `json:"projectId"`
	ProjectName       string         `json:"projectName,omitempty"`
	ScreenID          int64          `json:"screenId"`
	ScreenName        string         `json:"screenName,omitempty"`
	SKUID             int64          `json:"skuId"`
	SKUName           string         `json:"skuName,omitempty"`
	EventDay          string         `json:"eventDay"`
	EventDayConfirmed bool           `json:"eventDayConfirmed"`
	NeedsReview       bool           `json:"needsReview"`
	SmartMerge        bool           `json:"smartMerge"`
	OrderCapacity     int            `json:"orderCapacity"`
	CapacitySource    CapacitySource `json:"capacitySource"`
	Priority          int            `json:"priority"`
	PrimaryWorkerIDs  []string       `json:"primaryWorkerIds,omitempty"`
	StandbyWorkerIDs  []string       `json:"standbyWorkerIds,omitempty"`
	StartAt           time.Time      `json:"startAt"`
	Deadline          time.Time      `json:"deadline"`
}

func (m MacroTask) EffectiveCapacity() int {
	if m.OrderCapacity > 0 {
		return m.OrderCapacity
	}
	return 4
}

func (m MacroTask) Dispatchable() bool {
	return !m.NeedsReview && m.ProjectID > 0 && m.ScreenID > 0 && m.SKUID > 0
}

type PurchaseGroup struct {
	ID          string    `json:"id"`
	MacroTaskID string    `json:"macroTaskId"`
	Buyers      []Buyer   `json:"buyers"`
	AllowSplit  bool      `json:"allowSplit"`
	Weight      int       `json:"weight"`   // relative worker/account share (default=1)
	Priority    int       `json:"priority"` // lower values receive remainder slots first
	CreatedAt   time.Time `json:"createdAt"`
}

type LogicalOrderIntent struct {
	ID              string        `json:"id"`
	MacroTaskID     string        `json:"macroTaskId"`
	PurchaseGroupID string        `json:"purchaseGroupId,omitempty"` // originating purchase group
	Phase           Phase         `json:"phase"`
	Buyers          []Buyer       `json:"buyers"`
	BuyerDays       []BuyerDayKey `json:"buyerDays"`
	ShapeHash       string        `json:"shapeHash"`
	Succeeded       bool          `json:"succeeded"`
	Armed           bool          `json:"armed"`
	Terminal        bool          `json:"terminal"`
	Weight          int           `json:"weight"`   // relative worker/account share
	Priority        int           `json:"priority"` // lower values receive remainder slots first
	FailureReason   FailureReason `json:"failureReason,omitempty"`
	CreatedAt       time.Time     `json:"createdAt"`
}

func NewIntent(id string, macro MacroTask, phase Phase, buyers []Buyer, now time.Time) (LogicalOrderIntent, error) {
	if len(buyers) == 0 || len(buyers) > macro.EffectiveCapacity() {
		return LogicalOrderIntent{}, fmt.Errorf("buyer count %d exceeds capacity %d", len(buyers), macro.EffectiveCapacity())
	}
	if macro.EventDay == "" {
		return LogicalOrderIntent{}, fmt.Errorf("event day is empty")
	}
	ordered := append([]Buyer(nil), buyers...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].LogicalID < ordered[j].LogicalID })
	keys := make([]BuyerDayKey, len(ordered))
	for i, b := range ordered {
		keys[i] = BuyerDayKey{BuyerID: b.LogicalID, EventDay: macro.EventDay}
	}
	shape, _ := json.Marshal(struct {
		Macro  string
		Phase  Phase
		Buyers []string
	}{macro.ID, phase, buyerIDs(ordered)})
	sum := sha256.Sum256(shape)
	return LogicalOrderIntent{ID: id, MacroTaskID: macro.ID, Phase: phase, Buyers: ordered, BuyerDays: keys, ShapeHash: hex.EncodeToString(sum[:]), Armed: true, CreatedAt: now}, nil
}

func buyerIDs(b []Buyer) []string {
	ids := make([]string, len(b))
	for i := range b {
		ids[i] = b[i].LogicalID
	}
	return ids
}

func Conflicts(a, b LogicalOrderIntent) bool {
	set := make(map[string]struct{}, len(a.BuyerDays))
	for _, k := range a.BuyerDays {
		set[k.String()] = struct{}{}
	}
	for _, k := range b.BuyerDays {
		if _, ok := set[k.String()]; ok {
			return true
		}
	}
	return false
}

type Credentials struct {
	Cookies       map[string]string `json:"cookies"`
	CookieJar     []HTTPCookie      `json:"cookieJar,omitempty"`
	RefreshToken  string            `json:"refreshToken,omitempty"`
	Version       int64             `json:"version"`
	DeviceProfile json.RawMessage   `json:"deviceProfile,omitempty"`
}

type HTTPCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HTTPOnly bool   `json:"httpOnly,omitempty"`
	Expires  int64  `json:"expires,omitempty"`
}

type Account struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Credentials   Credentials `json:"credentials"`
	Tags          []string    `json:"tags,omitempty"`
	CooldownUntil time.Time   `json:"cooldownUntil,omitempty"`
	Enabled       bool        `json:"enabled"`
	VipStatus     int         `json:"vipStatus"` // 0=unknown, 1=VIP
}

type AccountBuyerMapping struct {
	AccountID      string    `json:"accountId"`
	LogicalBuyerID string    `json:"logicalBuyerId"`
	BuyerID        int64     `json:"buyerId"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type WorkerType string

const (
	WorkerTypeLocal  WorkerType = "local"
	WorkerTypeRemote WorkerType = "remote"
)

type WorkerNode struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Address          string     `json:"address"`
	Type             WorkerType `json:"type"`
	Tags             []string   `json:"tags,omitempty"`
	Version          string     `json:"version,omitempty"`
	Enabled          bool       `json:"enabled"`
	TLSServerName    string     `json:"tlsServerName,omitempty"`
	SkipVersionCheck bool       `json:"skipVersionCheck"` // true = bypass protocol version check on every Health call
	LastSeen         time.Time  `json:"lastSeen,omitempty"`
}

// WorkerTLSConfig holds the mTLS material for connecting to a worker.
type WorkerTLSConfig struct {
	CACertPEM     []byte `json:"caCert"`
	ClientCertPEM []byte `json:"clientCert"`
	ClientKeyPEM  []byte `json:"clientKey"`
	ServerName    string `json:"serverName,omitempty"`
}

type Lease struct {
	ID        string    `json:"id"`
	AttemptID string    `json:"attemptId"`
	AccountID string    `json:"accountId"`
	WorkerID  string    `json:"workerId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type ExecutionAttempt struct {
	ID        string          `json:"id"`
	IntentID  string          `json:"intentId"`
	SpecHash  string          `json:"specHash"`
	AccountID string          `json:"accountId"`
	WorkerID  string          `json:"workerId"`
	State     AttemptState    `json:"state"`
	Result    ExecutionResult `json:"result,omitempty"`
	Lease     Lease           `json:"lease"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type ExecutionSpec struct {
	AttemptID    string      `json:"attemptId"`
	IntentID     string      `json:"intentId"`
	ProjectID    int64       `json:"projectId"`
	ScreenID     int64       `json:"screenId"`
	SKUID        int64       `json:"skuId"`
	Buyers       []Buyer     `json:"buyers"`
	StartMode    StartMode   `json:"startMode"`
	StartAt      time.Time   `json:"startAt,omitempty"`
	Deadline     time.Time   `json:"deadline"`
	IntervalMS   int64       `json:"intervalMs"`
	StartDelayMS int64       `json:"startDelayMs,omitempty"`
	Credentials  Credentials `json:"credentials"`
}

func (s ExecutionSpec) Validate() error {
	if strings.TrimSpace(s.AttemptID) == "" || strings.TrimSpace(s.IntentID) == "" {
		return fmt.Errorf("attemptId and intentId are required")
	}
	if s.ProjectID <= 0 || s.ScreenID <= 0 || s.SKUID <= 0 || len(s.Buyers) == 0 {
		return fmt.Errorf("invalid immutable order shape")
	}
	if s.StartMode != StartImmediate && s.StartMode != StartScheduled {
		return fmt.Errorf("invalid start mode")
	}
	if s.StartMode == StartScheduled && s.StartAt.IsZero() {
		return fmt.Errorf("scheduled task requires startAt")
	}
	if s.Deadline.IsZero() {
		return fmt.Errorf("deadline is required")
	}
	return nil
}

func (s ExecutionSpec) Hash() string {
	copy := s
	copy.Credentials = Credentials{}
	b, _ := json.Marshal(copy)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type ExecutionResult struct {
	AttemptID     string        `json:"attemptId"`
	IntentID      string        `json:"intentId"`
	SpecHash      string        `json:"specHash"`
	State         AttemptState  `json:"state"`
	Success       bool          `json:"success"`
	OrderID       string        `json:"orderId,omitempty"`
	PaymentURL    string        `json:"paymentUrl,omitempty"`
	PaymentExpire int64         `json:"paymentExpire,omitempty"`
	OrderTime     int64         `json:"orderTime,omitempty"`
	Reason        FailureReason `json:"reason,omitempty"`
	Message       string        `json:"message,omitempty"`
	Retryable     bool          `json:"retryable"`
	Credentials   Credentials   `json:"credentials"`
	StartedAt     time.Time     `json:"startedAt,omitempty"`
	FinishedAt    time.Time     `json:"finishedAt,omitempty"`
}
