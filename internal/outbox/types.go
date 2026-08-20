package outbox

import (
	"errors"
	"time"
)

const (
	SchemaVersion                    = 1
	DefaultMaxEvents                 = 10_000
	DefaultMaxBytes            int64 = 64 * 1024 * 1024
	DefaultStageMaxEvents            = 256
	DefaultStageMaxBytes       int64 = 2 * 1024 * 1024
	DefaultQuarantineRetention       = 30 * 24 * time.Hour
)

type State string

const (
	StatePending     State = "pending"
	StateInflight    State = "inflight"
	StateQuarantined State = "quarantined"
)

var (
	ErrDeliveryExists   = errors.New("outbox delivery already exists")
	ErrDeliveryNotFound = errors.New("outbox delivery not found")
	ErrOutboxFull       = errors.New("outbox storage bound reached")
	ErrUnknownSchema    = errors.New("outbox schema version is unsupported")
)

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
	Endpoint                string    `json:"endpoint"`
	State                   State     `json:"state"`
	Sequence                uint64    `json:"sequence"`
	EnqueuedAt              time.Time `json:"enqueuedAt"`
	NextAttemptAt           time.Time `json:"nextAttemptAt"`
	Attempts                int       `json:"attempts"`
	LastStatusCode          int       `json:"lastStatusCode,omitempty"`
	LastErrorCode           string    `json:"lastErrorCode,omitempty"`
	LastError               string    `json:"lastError,omitempty"`
	QuarantinedAt           time.Time `json:"quarantinedAt,omitempty"`
	QuarantineReason        string    `json:"quarantineReason,omitempty"`
}

type AttemptFailure struct {
	StatusCode int
	ErrorCode  string
	Message    string
}

type Stats struct {
	PendingCount     int
	QuarantinedCount int
	TotalCount       int
	UsedBytes        int64
	OldestPendingAt  *time.Time
	Recovered        bool
	RecoveryCode     string
}
