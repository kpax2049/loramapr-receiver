package outbox

import (
	"errors"
	"time"
)

const (
	SchemaVersion                        = 1
	DefaultMaxEvents                     = 10_000
	DefaultMaxBytes                int64 = 64 * 1024 * 1024
	DefaultStageMaxEvents                = 256
	DefaultStageMaxBytes           int64 = 2 * 1024 * 1024
	DefaultQuarantineRetention           = 30 * 24 * time.Hour
	DefaultQuarantinePruneInterval       = time.Hour
)

type State string

const (
	StatePending     State = "pending"
	StateInflight    State = "inflight"
	StateQuarantined State = "quarantined"
)

var (
	ErrDeliveryExists        = errors.New("outbox delivery already exists")
	ErrDeliveryNotFound      = errors.New("outbox delivery not found")
	ErrDispatchPauseMismatch = errors.New("outbox dispatch pause does not match delivery")
	ErrOutboxFull            = errors.New("outbox storage bound reached")
	ErrUnknownSchema         = errors.New("outbox schema version is unsupported")
	ErrOutboxPruneFailed     = errors.New("outbox_prune_failed")
)

type DispatchPauseKind string

const (
	DispatchPauseCollision  DispatchPauseKind = "delivery_id_collision"
	DispatchPauseCredential DispatchPauseKind = "credential_rejected"
	DispatchPauseBinding    DispatchPauseKind = "receiver_binding_rejected"
)

type DispatchPause struct {
	Kind                 DispatchPauseKind `json:"kind"`
	Reason               string            `json:"reason"`
	DeliveryID           string            `json:"deliveryId"`
	PausedAt             time.Time         `json:"pausedAt"`
	CredentialGeneration uint64            `json:"credentialGeneration,omitempty"`
	BindingGeneration    uint64            `json:"bindingGeneration,omitempty"`
	StatusCode           int               `json:"statusCode,omitempty"`
	ErrorCode            string            `json:"errorCode,omitempty"`
	RequestID            string            `json:"requestId,omitempty"`
}

type InstallationRotationPhase string

const (
	InstallationRotationIntent                InstallationRotationPhase = "intent"
	InstallationRotationOldBindingQuarantined InstallationRotationPhase = "old_binding_quarantined"
	InstallationRotationStateRotated          InstallationRotationPhase = "state_rotated"
	InstallationRotationCompleted             InstallationRotationPhase = "completed"
)

type InstallationRotation struct {
	OldInstallationID string                    `json:"oldInstallationId"`
	NewInstallationID string                    `json:"newInstallationId"`
	Reason            string                    `json:"reason"`
	Phase             InstallationRotationPhase `json:"phase"`
	StartedAt         time.Time                 `json:"startedAt"`
	UpdatedAt         time.Time                 `json:"updatedAt"`
	Quarantined       int                       `json:"quarantined"`
}

type Config struct {
	Path                string
	MaxEvents           int
	MaxBytes            int64
	QuarantineRetention time.Duration
	LockTimeout         time.Duration
	Now                 func() time.Time
}

type Delivery struct {
	DeliveryID              string    `json:"deliveryId"`
	Envelope                []byte    `json:"envelope"`
	EnvelopeSHA256          string    `json:"envelopeSha256"`
	IdempotencyKey          string    `json:"idempotencyKey"`
	OwnerID                 string    `json:"ownerId"`
	ReceiverAgentIDSnapshot string    `json:"receiverAgentIdSnapshot"`
	InstallationID          string    `json:"installationId"`
	CredentialGeneration    uint64    `json:"credentialGeneration,omitempty"`
	BindingGeneration       uint64    `json:"bindingGeneration,omitempty"`
	Endpoint                string    `json:"endpoint"`
	State                   State     `json:"state"`
	Sequence                uint64    `json:"sequence"`
	EnqueuedAt              time.Time `json:"enqueuedAt"`
	NextAttemptAt           time.Time `json:"nextAttemptAt"`
	Attempts                int       `json:"attempts"`
	LastStatusCode          int       `json:"lastStatusCode,omitempty"`
	LastErrorCode           string    `json:"lastErrorCode,omitempty"`
	LastError               string    `json:"lastError,omitempty"`
	LastRequestID           string    `json:"lastRequestId,omitempty"`
	QuarantinedAt           time.Time `json:"quarantinedAt,omitempty"`
	QuarantineReason        string    `json:"quarantineReason,omitempty"`
}

type AttemptFailure struct {
	StatusCode int
	ErrorCode  string
	Message    string
	RequestID  string
}

type Binding struct {
	OwnerID              string
	ReceiverAgentID      string
	InstallationID       string
	CredentialGeneration uint64
	BindingGeneration    uint64
}

type BindingReconcileResult struct {
	Kept                         int
	CredentialRebindQuarantined  int
	InstallationResetQuarantined int
}

type Stats struct {
	PendingCount         int
	QuarantinedCount     int
	TotalCount           int
	UsedBytes            int64
	Recovered            bool
	RecoveryCode         string
	MaintenanceErrorCode string
	MaintenanceError     string
	DispatchPause        *DispatchPause
}
