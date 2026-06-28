package cluster_service

import (
	"context"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/accounts"
	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/employer"
	clusterstorage "bilibili-ticket-golang/cluster/storage"
	"bilibili-ticket-golang/lib/biliutils"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ClusterSnapshot is the full state snapshot sent to the frontend.
type ClusterSnapshot struct {
	TaskGroups       []domain.TaskGroup  `json:"taskGroups"`
	Accounts         []AccountSummary    `json:"accounts"`
	Buyers           []BuyerWithAccounts `json:"buyers"`
	Workers          []WorkerSummary     `json:"workers"`
	Macros           []MacroSummary      `json:"macros"`
	Intents          []IntentSummary     `json:"intents"`
	Attempts         []AttemptSummary    `json:"attempts"`
	BilibiliOffsetMs int64               `json:"bilibiliOffsetMs"` // employer clock offset to Bilibili API (ms)
	NtpOffsetMs      int64               `json:"ntpOffsetMs"`      // employer clock offset to NTP (ms)
}

// BuyerAccountBadge represents an account that owns a particular buyer.
type BuyerAccountBadge struct {
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	UID         string `json:"uid"`
}

// BuyerWithAccounts extends domain.Buyer with the list of accounts that
// have this buyer in their real-name list.
type BuyerWithAccounts struct {
	domain.Buyer
	Accounts []BuyerAccountBadge `json:"accounts"`
}

// AccountSummary is a lightweight view of an account for the frontend.
type AccountSummary struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Enabled           bool       `json:"enabled"`
	VipStatus         int        `json:"vipStatus"` // 0=unknown/not VIP, 1=VIP
	CooldownUntil     *time.Time `json:"cooldownUntil,omitempty"`
	CooldownReason    string     `json:"cooldownReason,omitempty"`
	CredentialVersion int64      `json:"credentialVersion"`
}

// WorkerCooldownInfo describes why and until when a worker is cooled down.
type WorkerCooldownInfo struct {
	CooledDown      bool      `json:"cooledDown"`
	CooldownEnd     time.Time `json:"cooldownEnd,omitempty"`
	StartedAt       time.Time `json:"startedAt,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	RemainingMs     int64     `json:"remainingMs"`     // milliseconds remaining, 0 when not cooled down
	TotalDurationMs int64     `json:"totalDurationMs"` // total cooldown duration in ms
}

// WorkerSummary is a lightweight view of a worker for the frontend.
type WorkerSummary struct {
	ID                   string             `json:"id"`
	Name                 string             `json:"name"`
	Address              string             `json:"address"`
	Type                 domain.WorkerType  `json:"type"`
	Enabled              bool               `json:"enabled"`
	Healthy              bool               `json:"healthy"`
	ActiveAttemptID      string             `json:"activeAttemptId,omitempty"`
	Version              string             `json:"version,omitempty"`
	Cooldown             WorkerCooldownInfo `json:"cooldown,omitempty"`
	LastHeartbeatAt      *time.Time         `json:"lastHeartbeatAt,omitempty"`
	LastHeartbeatLatency int64              `json:"lastHeartbeatLatencyMs"` // ms since last heartbeat
}

// MacroSummary extends a macro task with its phase and purchase groups.
type MacroSummary struct {
	domain.MacroTask
	Phase          domain.Phase           `json:"phase"`
	PurchaseGroups []domain.PurchaseGroup `json:"purchaseGroups"`
}

// AttemptSummary is a lightweight view of an execution attempt for the UI.
type AttemptSummary struct {
	ID         string               `json:"id"`
	IntentID   string               `json:"intentId"`
	AccountID  string               `json:"accountId"`
	WorkerID   string               `json:"workerId"`
	State      domain.AttemptState  `json:"state"`
	OrderID    string               `json:"orderId,omitempty"`
	PaymentURL string               `json:"paymentUrl,omitempty"`
	Reason     domain.FailureReason `json:"reason,omitempty"`
}

// IntentSummary exposes an armed intent for the UI dispatch log.
type IntentSummary struct {
	ID            string               `json:"id"`
	MacroTaskID   string               `json:"macroTaskId"`
	Phase         domain.Phase         `json:"phase"`
	Weight        int                  `json:"weight"`
	Priority      int                  `json:"priority"`
	BuyerCount    int                  `json:"buyerCount"`
	Succeeded     bool                 `json:"succeeded"`
	Terminal      bool                 `json:"terminal"`
	Armed         bool                 `json:"armed"`
	ActiveCount   int                  `json:"activeCount"` // non-terminal attempts currently running
	Deficit       int                  `json:"deficit"`     // remaining attempts to reach current proportional target
	FailureReason domain.FailureReason `json:"failureReason,omitempty"`
	CreatedAt     time.Time            `json:"createdAt"`
}

// ClusterService is the top-level orchestrator: it wires together the
// dispatcher, account manager, worker client, and local worker manager,
// and exposes high-level operations to the Wails frontend.
type ClusterService struct {
	repository    *clusterstorage.Repository
	client        *employer.WorkerClient
	dispatcher    *dispatcher.Dispatcher
	accounts      *accounts.Manager
	local         employer.LocalWorkerManager
	mu            sync.RWMutex
	mainAccountMu sync.Mutex
	phases        map[string]domain.Phase
	loginSessions map[string]*accountLoginSession
	catalog       *biliutils.BiliClient
	cancel        context.CancelFunc
	notify        func(string)
	wailsApp      *application.App
}
