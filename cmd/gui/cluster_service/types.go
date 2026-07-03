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
	TaskGroups      []domain.TaskGroup  `json:"taskGroups"`
	Accounts        []AccountSummary    `json:"accounts"`
	Buyers          []BuyerWithAccounts `json:"buyers"`
	Workers         []WorkerSummary     `json:"workers"`
	Macros          []MacroSummary      `json:"macros"`
	Intents         []IntentSummary     `json:"intents"`
	Attempts        []AttemptSummary    `json:"attempts"`
	ActiveTaskGroup string              `json:"activeTaskGroup,omitempty"`
	EmployerVersion string              `json:"employerVersion"` // employer build version (git commit hash)
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
	Tags              []string   `json:"tags,omitempty"`
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
	Tags                 []string           `json:"tags,omitempty"`
	Enabled              bool               `json:"enabled"`
	Healthy              bool               `json:"healthy"`
	SkipVersionCheck     bool               `json:"skipVersionCheck"`
	VersionBlocked       bool               `json:"versionBlocked"` // Health passed but protocol version mismatched
	ActiveAttemptID      string             `json:"activeAttemptId,omitempty"`
	Version              string             `json:"version,omitempty"`
	BilibiliOffsetMs     int64              `json:"bilibiliOffsetMs"` // worker clock offset to Bilibili API (ms)
	NtpOffsetMs          int64              `json:"ntpOffsetMs"`      // worker clock offset to NTP (ms)
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
	ID                  string               `json:"id"`
	IntentID            string               `json:"intentId"`
	AccountID           string               `json:"accountId"`
	WorkerID            string               `json:"workerId"`
	State               domain.AttemptState  `json:"state"`
	OrderID             string               `json:"orderId,omitempty"`
	PaymentURL          string               `json:"paymentUrl,omitempty"`
	Reason              domain.FailureReason `json:"reason,omitempty"`
	CooldownRemainingMs int64                `json:"cooldownRemainingMs"` // >0 when cooling, remaining milliseconds
}

// ClusterEventKind categorises a cluster-wide event for the unified log.
type ClusterEventKind string

const (
	EventWorkerConnected    ClusterEventKind = "worker_connected"
	EventWorkerDisconnected ClusterEventKind = "worker_disconnected"
	EventWorkerHealthy      ClusterEventKind = "worker_healthy"
	EventWorkerUnhealthy    ClusterEventKind = "worker_unhealthy"
	EventTaskCompleted      ClusterEventKind = "task_completed"
	EventTaskFailed         ClusterEventKind = "task_failed"
	EventTaskSuperseded     ClusterEventKind = "task_superseded"
	EventTaskStopped        ClusterEventKind = "task_stopped"
	EventHeartbeatTimeout   ClusterEventKind = "heartbeat_timeout"
	EventHeartbeatLatency   ClusterEventKind = "heartbeat_latency"
	EventWorkerInfo         ClusterEventKind = "worker_info"
	EventDispatchInfo       ClusterEventKind = "dispatch_info"
	EventDispatchWarning    ClusterEventKind = "dispatch_warning"
)

// ClusterEvent is a single entry in the unified cluster event feed.
type ClusterEvent struct {
	Time      time.Time        `json:"time"`
	Kind      ClusterEventKind `json:"kind"`
	WorkerID  string           `json:"workerId"`
	Stage     string           `json:"stage"`
	Message   string           `json:"message"`
	OrderID   string           `json:"orderId,omitempty"`
	AttemptID string           `json:"attemptId,omitempty"`
	Code      int              `json:"code"`
	Retryable bool             `json:"retryable"`
}

// ClusterEventLog is the accumulation of cluster-scoped events pushed to
// the frontend on demand.
type ClusterEventLog struct {
	Events []ClusterEvent `json:"events"`
}

// OrderRecordList is returned to the frontend order history page.
type OrderRecordList struct {
	Records []domain.OrderRecord `json:"records"`
}

// IntentSummary exposes an armed intent for the UI dispatch log.
type IntentSummary struct {
	ID              string               `json:"id"`
	MacroTaskID     string               `json:"macroTaskId"`
	PurchaseGroupID string               `json:"purchaseGroupId,omitempty"`
	Phase           domain.Phase         `json:"phase"`
	Weight          int                  `json:"weight"`
	Priority        int                  `json:"priority"`
	BuyerCount      int                  `json:"buyerCount"`
	Succeeded       bool                 `json:"succeeded"`
	Terminal        bool                 `json:"terminal"`
	Armed           bool                 `json:"armed"`
	ActiveCount     int                  `json:"activeCount"` // non-terminal attempts currently running
	Deficit         int                  `json:"deficit"`     // remaining attempts to reach current proportional target
	FailureReason   domain.FailureReason `json:"failureReason,omitempty"`
	CreatedAt       time.Time            `json:"createdAt"`
}

// ClusterService is the top-level orchestrator: it wires together the
// dispatcher, account manager, worker client, and local worker manager,
// and exposes high-level operations to the Wails frontend.
type ClusterService struct {
	repository      *clusterstorage.Repository
	client          *employer.WorkerClient
	dispatcher      *dispatcher.Dispatcher
	accounts        *accounts.Manager
	provisioner     *WorkerProvisioner
	local           employer.LocalWorkerManager
	mu              sync.RWMutex
	waveMu          sync.Mutex
	waveCancels     map[string]context.CancelFunc
	mainAccountMu   sync.Mutex
	phases          map[string]domain.Phase
	loginSessions   map[string]*accountLoginSession
	catalog         *biliutils.BiliClient
	cancel          context.CancelFunc
	notify          func(string)
	wailsApp        *application.App
	globalCfg       globalConfig        // pushed to all workers via Configure RPC
	workers         []domain.WorkerNode // cached worker list for provisioner
	eventLog        []ClusterEvent      // aggregated event feed (ring buffer)
	accountBindings map[string]string   // accountID → workerID (mutual exclusion)
	deployMu        sync.RWMutex
	deployJobs      map[string]*RemoteWorkerDeployJob
	bwsMeta         map[string]BWSSubmitInput // attemptID → BWS submit metadata
}
