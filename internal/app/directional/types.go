package directional

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	MaxQueueCapacity = 8
	MaxConcurrency   = 32
)

var (
	ErrQueueFull           = errors.New("directional queue is full")
	ErrNotFound            = errors.New("directional job not found")
	ErrNotStarted          = errors.New("directional coordinator is not started")
	ErrClosed              = errors.New("directional coordinator is closed")
	ErrIdempotencyConflict = errors.New("directional idempotency key conflicts with another request")
	ErrOwnerBusy           = errors.New("directional owner already has an active job")
	ErrUnavailable         = errors.New("directional analysis is unavailable")
	ErrDisabled            = errors.New("directional analysis is disabled")
)

type Kind string

const (
	KindHorizon   Kind = "horizon"
	KindAstrodome Kind = "astrodome"
)

type State string

const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateReady     State = "ready"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Request is platform-neutral. Payload is interpreted only by the registered
// runner; the coordinator neither serializes it nor knows coordinates/models.
type Request struct {
	Kind            Kind
	OwnerID         string
	IdempotencyKey  string
	ScienceCacheKey string
	Source          SourceIdentity
	Payload         json.RawMessage
}

type SourceIdentity struct {
	Provider       string
	RunID          string
	GridProfile    string
	GeometryDigest string
}

// Execution gives a runner one private workspace. DatasetPath returned by the
// runner must resolve to a regular file below this directory.
type Execution struct {
	JobID     string
	Kind      Kind
	Source    SourceIdentity
	Payload   json.RawMessage
	Workspace string
}

// RunnerResult describes a completed temporary dataset. The coordinator owns
// validation, hashing and immutable atomic publication.
type RunnerResult struct {
	DatasetPath     string
	Provider        string
	RunID           string
	GridProfile     string
	GeometryDigest  string
	ContentEncoding string
}

type Runner interface {
	Run(context.Context, Execution) (RunnerResult, error)
}

type RunnerFunc func(context.Context, Execution) (RunnerResult, error)

func (function RunnerFunc) Run(ctx context.Context, execution Execution) (RunnerResult, error) {
	return function(ctx, execution)
}

// Result is immutable published science output. Path is an internal local path
// and is intentionally omitted from public HTTP DTOs.
type Result struct {
	Path            string
	Bytes           int64
	ETag            string
	ContentEncoding string
	Provider        string
	RunID           string
	GridProfile     string
	GeometryDigest  string
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

type JobStatus struct {
	ID             string
	Kind           Kind
	State          State
	QueuePosition  *int
	EstimatedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Provider       string
	RunID          string
	GridProfile    string
	GeometryDigest string
	DatasetBytes   int64
	FailureCode    string
}

type QueueStats struct {
	QueueLength int
	Running     bool
}

type KindPolicy struct {
	Estimate time.Duration
	Timeout  time.Duration
}

type Config struct {
	QueueCapacity  int
	Concurrency    int
	CompletedLimit int
	CompletedTTL   time.Duration
	ResultRoot     string
	Policies       map[Kind]KindPolicy
	Now            func() time.Time
}

func (config Config) validate() error {
	if config.QueueCapacity < 1 || config.QueueCapacity > MaxQueueCapacity {
		return fmt.Errorf("directional queue capacity must be between 1 and %d", MaxQueueCapacity)
	}
	if config.Concurrency < 1 || config.Concurrency > MaxConcurrency {
		return fmt.Errorf("directional concurrency must be between 1 and %d", MaxConcurrency)
	}
	if config.CompletedLimit < 1 {
		return errors.New("directional completed metadata limit must be positive")
	}
	if config.CompletedTTL <= 0 {
		return errors.New("directional completed metadata TTL must be positive")
	}
	if config.ResultRoot == "" {
		return errors.New("directional result root is required")
	}
	for _, kind := range []Kind{KindHorizon, KindAstrodome} {
		policy, exists := config.Policies[kind]
		if !exists || policy.Estimate <= 0 || policy.Timeout < 0 {
			return fmt.Errorf("positive estimate and non-negative timeout are required for %s", kind)
		}
	}
	return nil
}

func validKind(kind Kind) bool {
	return kind == KindHorizon || kind == KindAstrodome
}

// FailureCoder lets a runner expose a stable, non-sensitive failure code.
type FailureCoder interface {
	FailureCode() string
}

type CodedError struct {
	Code string
	Err  error
}

func (err CodedError) Error() string {
	if err.Err == nil {
		return err.Code
	}
	return err.Err.Error()
}

func (err CodedError) Unwrap() error       { return err.Err }
func (err CodedError) FailureCode() string { return err.Code }

// Astrodome internal HTTP DTOs intentionally mirror astroweb's gateway
// contract. Admission retains Go's default exported field names because that
// is what gateway_client.go currently emits.
type SavedPoint struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type AstrodomeAdmission struct {
	TelegramUserID int64
	Point          SavedPoint
	Language       string
	IdempotencyKey string
}

type PreparedAstrodome struct {
	ScienceCacheKey string
	Source          SourceIdentity
	Payload         json.RawMessage
}

type AstrodomeAvailability struct {
	Enabled          bool      `json:"enabled"`
	Available        bool      `json:"available"`
	WorkerAvailable  bool      `json:"worker_available"`
	Provider         string    `json:"provider,omitempty"`
	RunID            string    `json:"run_id,omitempty"`
	RunBaseTime      time.Time `json:"run_base_time,omitempty"`
	FreshnessSeconds int64     `json:"freshness_seconds,omitempty"`
	Stale            bool      `json:"stale"`
	GridProfile      string    `json:"grid_profile,omitempty"`
	QueueLength      int       `json:"queue_length"`
	Running          bool      `json:"running"`
	Reason           string    `json:"reason,omitempty"`
}

type AstrodomeBackend interface {
	Availability(context.Context, int64) (AstrodomeAvailability, error)
	Prepare(context.Context, AstrodomeAdmission) (PreparedAstrodome, error)
}

type WorkerHealth interface {
	Health(context.Context) error
}

// JSONPayload is a convenience for adapters whose registered runner expects
// immutable JSON request bytes.
func JSONPayload(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}
