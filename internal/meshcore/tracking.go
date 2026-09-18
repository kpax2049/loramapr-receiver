package meshcore

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
)

// MotionState is an operational scheduler classification. It is never a
// statement about the authenticity or trust of a telemetry position.
type MotionState string

const (
	MotionUnknown    MotionState = "unknown"
	MotionStationary MotionState = "stationary"
	MotionSlow       MotionState = "slow"
	MotionFast       MotionState = "fast"
)

var (
	ErrTrackingAlreadyActive   = errors.New("MeshCore tracking is already active")
	ErrTrackingDifferentTarget = errors.New("MeshCore tracking is active for a different target")
	ErrTrackingUnavailable     = errors.New("MeshCore tracking controller is unavailable")
	ErrTrackingSessionManaged  = errors.New("MeshCore tracking is managed by an active LoRaMapr Session")
)

// TrackingPolicy centralizes the deliberately conservative M7A airtime policy.
// It is receiver-local and can be replaced by a stricter policy later.
type TrackingPolicy struct {
	UnknownInterval    time.Duration
	StationaryInterval time.Duration
	SlowInterval       time.Duration
	FastInterval       time.Duration
	MinimumInterval    time.Duration
	MaximumBackoff     time.Duration
	StationaryKmh      float64
	SlowKmh            float64
	MinimumSampleAge   time.Duration
	MaximumJumpMeters  float64
	MaximumSpeedKmh    float64
}

func DefaultTrackingPolicy() TrackingPolicy {
	return TrackingPolicy{
		UnknownInterval: 30 * time.Second, StationaryInterval: 45 * time.Second,
		SlowInterval: 30 * time.Second, FastInterval: 15 * time.Second,
		MinimumInterval: 10 * time.Second, MaximumBackoff: 5 * time.Minute,
		StationaryKmh: 1, SlowKmh: 8, MinimumSampleAge: 5 * time.Second,
		MaximumJumpMeters: 10000, MaximumSpeedKmh: 250,
	}
}

func (p TrackingPolicy) normalized() TrackingPolicy {
	d := DefaultTrackingPolicy()
	if p.UnknownInterval <= 0 {
		p.UnknownInterval = d.UnknownInterval
	}
	if p.StationaryInterval <= 0 {
		p.StationaryInterval = d.StationaryInterval
	}
	if p.SlowInterval <= 0 {
		p.SlowInterval = d.SlowInterval
	}
	if p.FastInterval <= 0 {
		p.FastInterval = d.FastInterval
	}
	if p.MinimumInterval <= 0 {
		p.MinimumInterval = d.MinimumInterval
	}
	if p.MaximumBackoff <= 0 {
		p.MaximumBackoff = d.MaximumBackoff
	}
	if p.StationaryKmh <= 0 {
		p.StationaryKmh = d.StationaryKmh
	}
	if p.SlowKmh <= p.StationaryKmh {
		p.SlowKmh = d.SlowKmh
	}
	if p.MinimumSampleAge <= 0 {
		p.MinimumSampleAge = d.MinimumSampleAge
	}
	if p.MaximumJumpMeters <= 0 {
		p.MaximumJumpMeters = d.MaximumJumpMeters
	}
	if p.MaximumSpeedKmh <= 0 {
		p.MaximumSpeedKmh = d.MaximumSpeedKmh
	}
	p.UnknownInterval = maxDuration(p.UnknownInterval, p.MinimumInterval)
	p.StationaryInterval = maxDuration(p.StationaryInterval, p.MinimumInterval)
	p.SlowInterval = maxDuration(p.SlowInterval, p.MinimumInterval)
	p.FastInterval = maxDuration(p.FastInterval, p.MinimumInterval)
	p.MaximumBackoff = maxDuration(p.MaximumBackoff, p.UnknownInterval)
	return p
}

// TrackingStatus is ephemeral operational state. The future Session lifecycle
// owner can bind directly to Start and Stop; it must not be persisted.
type TrackingStatus struct {
	Active                      bool        `json:"active"`
	TargetPublicKey             string      `json:"targetPublicKey,omitempty"`
	MotionState                 MotionState `json:"motionState"`
	EstimatedSpeedKmh           *float64    `json:"estimatedSpeedKmh"`
	CurrentIntervalSeconds      int64       `json:"currentIntervalSeconds"`
	LastRequestAt               *time.Time  `json:"lastRequestAt"`
	LastResponseAt              *time.Time  `json:"lastResponseAt"`
	TelemetryTransport          string      `json:"telemetryTransport,omitempty"`
	TelemetryCapability         string      `json:"telemetryCapability,omitempty"`
	LastTelemetryRequestTag     *uint32     `json:"lastTelemetryRequestTag,omitempty"`
	LastTelemetryResponseTag    *uint32     `json:"lastTelemetryResponseTag,omitempty"`
	LastTelemetryExpiredTag     *uint32     `json:"lastTelemetryExpiredTag,omitempty"`
	LastTelemetryFallbackReason string      `json:"lastTelemetryFallbackReason,omitempty"`
	LastFailureScheduleSource   string      `json:"lastFailureScheduleSource,omitempty"`
	NextRequestAt               *time.Time  `json:"nextRequestAt"`
	ConsecutiveFailures         int         `json:"consecutiveFailures"`
	LastError                   *string     `json:"lastError"`
	RouteRecoveryState          string      `json:"routeRecoveryState"`
	LastRouteRecoveryEvent      string      `json:"lastRouteRecoveryEvent"`
	RecoveryEpisodeActive       bool        `json:"recoveryEpisodeActive"`
	PathResetAttempts           int         `json:"pathResetAttempts"`
	StaleRouteFailures          int         `json:"staleRouteFailures"`
	StaleRouteFailureThreshold  int         `json:"staleRouteFailureThreshold"`
	RouteGeneration             uint64      `json:"routeGeneration"`
	RouteFingerprint            string      `json:"routeFingerprint"`
	LastPathUpdateAt            *time.Time  `json:"lastPathUpdateAt"`
	PathUpdatePending           bool        `json:"pathUpdatePending"`
	// LatestTelemetry is the latest prefix-correlated observation obtained by
	// this temporary receiver-local tracking harness. It is intentionally not
	// a signed current-position assertion.
	LatestTelemetry     *TelemetryResult `json:"latestTelemetry,omitempty"`
	RecentPolls         []TrackingPoll   `json:"recentPolls"`
	ControlSource       string           `json:"controlSource,omitempty"`
	SessionID           string           `json:"sessionId,omitempty"`
	DeviceID            string           `json:"deviceId,omitempty"`
	Desired             bool             `json:"desired"`
	IntentVersion       string           `json:"intentVersion,omitempty"`
	LastReconciledAt    *time.Time       `json:"lastReconciledAt,omitempty"`
	ReconciliationError *string          `json:"reconciliationError,omitempty"`
}

const (
	recentPollLimit            = 50
	staleRouteFailureThreshold = 3
)

// TrackingPoll is bounded, receiver-local route observability for field
// validation. ResponseRoute remains nil unless a future Companion payload
// proves a return route.
type TrackingPoll struct {
	RequestAt            time.Time      `json:"requestAt"`
	ResponseAt           *time.Time     `json:"responseAt,omitempty"`
	Outcome              string         `json:"outcome"`
	Error                *string        `json:"error,omitempty"`
	EstimatedSpeedKmh    *float64       `json:"estimatedSpeedKmh"`
	MotionState          MotionState    `json:"motionState"`
	IntervalSeconds      int64          `json:"intervalSeconds"`
	RouteAttempt         RouteEvidence  `json:"routeAttempt"`
	ResponseRoute        *RouteEvidence `json:"responseRoute"`
	ResponseRouteUnknown bool           `json:"responseRouteUnknown"`
	RouteRecovery        string         `json:"routeRecovery,omitempty"`
	StaleRouteFailures   int            `json:"staleRouteFailures"`
	PathUpdateObserved   bool           `json:"pathUpdateObserved"`
	ConsecutiveFailures  int            `json:"consecutiveFailures"`
	TelemetryTransport   string         `json:"telemetryTransport,omitempty"`
	RequestTag           *uint32        `json:"requestTag,omitempty"`
	ResponseTag          *uint32        `json:"responseTag,omitempty"`
	FallbackReason       string         `json:"fallbackReason,omitempty"`
	ScheduleSource       string         `json:"scheduleSource,omitempty"`
	RFEvidence           RFEvidence     `json:"rfEvidence"`
}

type telemetryRequester func(context.Context, string) (TelemetryResult, error)
type pathResetter func(context.Context, string) error

type trackingFix struct {
	latitude, longitude float64
	observedAt          time.Time
}

// TrackingController schedules correlated telemetry requests through the
// existing request path. It intentionally owns no radio transport or durable
// state, which keeps it ready to be lifecycle-owned by a cloud Session.
type TrackingController struct {
	request                  telemetryRequester
	reset                    pathResetter
	stageSuccessfulTelemetry func(TelemetryResult) error
	policy                   TrackingPolicy
	logger                   *slog.Logger
	now                      func() time.Time

	mu           sync.RWMutex
	status       TrackingStatus
	fix          *trackingFix
	pending      MotionState
	pendingCount int
	cancel       context.CancelFunc
	generation   uint64
	// controlSource is the authoritative owner used by both scheduling and the
	// exported status. Runtime must set it before starting/reusing a controller;
	// no separately decorated status source is permitted.
	controlSource         string
	scheduleChanged       chan struct{}
	routeFingerprint      string
	routeGeneration       uint64
	recoveryEpisodeActive bool
	pendingRecoveryEvent  string
	staleRouteFingerprint string
	staleRouteFailures    int
	pathResetAcknowledged bool
}

// SetSuccessfulTelemetryStager installs the receiver outbox seam for
// controller-owned polls. It is deliberately called before Start, so a
// correlated response is annotated with the current recovery facts before it
// becomes a durable normalized event.
func (c *TrackingController) SetSuccessfulTelemetryStager(stage func(TelemetryResult) error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.stageSuccessfulTelemetry = stage
	c.mu.Unlock()
}

func NewTrackingController(request func(context.Context, string) (TelemetryResult, error), policy TrackingPolicy, logger *slog.Logger) *TrackingController {
	return NewTrackingControllerWithRouteRecovery(request, nil, policy, logger)
}

// NewTrackingControllerWithRouteRecovery extends the local tracking harness
// with the single source-supported recovery action: reset a stale contact
// path and let Companion choose flood/path learning itself.
func NewTrackingControllerWithRouteRecovery(request func(context.Context, string) (TelemetryResult, error), reset func(context.Context, string) error, policy TrackingPolicy, logger *slog.Logger) *TrackingController {
	if logger == nil {
		logger = slog.Default()
	}
	p := policy.normalized()
	return &TrackingController{request: request, reset: reset, policy: p, logger: logger.With("component", "meshcore_tracking"), now: func() time.Time { return time.Now().UTC() }, scheduleChanged: make(chan struct{}, 1), status: TrackingStatus{MotionState: MotionUnknown, CurrentIntervalSeconds: int64(p.UnknownInterval / time.Second), StaleRouteFailureThreshold: staleRouteFailureThreshold, RecentPolls: []TrackingPoll{}}}
}

// SetControlSource sets the single authoritative owner for polling policy and
// status. A Session takeover wakes a pending manual delay so remote RF loss
// resumes motion-based discovery immediately.
func (c *TrackingController) SetControlSource(source string) {
	if c == nil {
		return
	}
	source = strings.ToLower(strings.TrimSpace(source))
	if source != "session" && source != "manual" {
		source = ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.controlSource == source {
		return
	}
	c.controlSource, c.status.ControlSource = source, source
	if source != "session" || !c.status.Active || (c.status.LastError != nil && *c.status.LastError == ErrTelemetryAdapterDisconnected.Error()) {
		return
	}
	c.status.CurrentIntervalSeconds = int64(c.intervalForLocked() / time.Second)
	c.signalScheduleChangedLocked()
}

// SetSessionManaged remains for callers outside this package; new runtime
// reconciliation uses SetControlSource so status and policy cannot diverge.
func (c *TrackingController) SetSessionManaged(managed bool) {
	if managed {
		c.SetControlSource("session")
		return
	}
	c.SetControlSource("")
}

func (c *TrackingController) ControlSource() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.controlSource
}

func (c *TrackingController) signalScheduleChangedLocked() {
	select {
	case c.scheduleChanged <- struct{}{}:
	default:
	}
}

func (c *TrackingController) clearScheduleChangedLocked() {
	for {
		select {
		case <-c.scheduleChanged:
		default:
			return
		}
	}
}

func (c *TrackingController) Start(publicKey string) (TrackingStatus, error) {
	if _, err := parseTelemetryTarget(publicKey); err != nil {
		return TrackingStatus{}, err
	}
	if c == nil || c.request == nil {
		return TrackingStatus{}, ErrTrackingUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status.Active {
		if c.status.TargetPublicKey != publicKey {
			return c.statusCopyLocked(), ErrTrackingDifferentTarget
		}
		return c.statusCopyLocked(), ErrTrackingAlreadyActive
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.generation++
	generation := c.generation
	c.fix, c.pending, c.pendingCount = nil, MotionUnknown, 0
	c.routeFingerprint, c.routeGeneration, c.recoveryEpisodeActive, c.pendingRecoveryEvent = "", 0, false, ""
	c.staleRouteFingerprint, c.staleRouteFailures = "", 0
	c.pathResetAcknowledged = false
	c.clearScheduleChangedLocked()
	c.status = TrackingStatus{Active: true, TargetPublicKey: publicKey, MotionState: MotionUnknown, CurrentIntervalSeconds: int64(c.policy.UnknownInterval / time.Second), ControlSource: c.controlSource, RouteRecoveryState: "idle", StaleRouteFailureThreshold: staleRouteFailureThreshold, RecentPolls: []TrackingPoll{}}
	now := c.now().UTC()
	c.status.NextRequestAt = timePtr(now)
	c.logger.Info("MeshCore tracking started", "target_public_key", publicKey)
	go c.run(ctx, publicKey, generation)
	return c.statusCopyLocked(), nil
}

// Stop cancels waiting between polls, but intentionally does not cancel an
// already-issued telemetry request. A response already arriving on the link
// remains eligible for the existing correlation/outbox path.
func (c *TrackingController) Stop() TrackingStatus {
	if c == nil {
		return TrackingStatus{MotionState: MotionUnknown}
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	wasActive, target := c.status.Active, c.status.TargetPublicKey
	c.generation++
	c.status.Active, c.status.TargetPublicKey, c.status.NextRequestAt = false, "", nil
	c.status.CurrentIntervalSeconds = int64(c.policy.UnknownInterval / time.Second)
	// A future tracking run must not inherit a stale-route episode from the
	// previous target/run. In particular, Release uses Stop before handing the
	// Companion to another client.
	c.fix, c.pending, c.pendingCount = nil, MotionUnknown, 0
	c.routeFingerprint, c.routeGeneration, c.recoveryEpisodeActive, c.pendingRecoveryEvent = "", 0, false, ""
	c.staleRouteFingerprint, c.staleRouteFailures = "", 0
	c.pathResetAcknowledged = false
	c.status.RouteRecoveryState = "idle"
	c.status.LastRouteRecoveryEvent = ""
	c.status.RecoveryEpisodeActive = false
	c.status.PathResetAttempts = 0
	c.status.StaleRouteFailures = 0
	c.status.StaleRouteFailureThreshold = staleRouteFailureThreshold
	c.status.RouteGeneration = 0
	c.status.RouteFingerprint = ""
	c.status.LastPathUpdateAt = nil
	c.status.PathUpdatePending = false
	c.clearScheduleChangedLocked()
	c.mu.Unlock()
	if wasActive {
		c.logger.Info("MeshCore tracking stopped", "target_public_key", target)
	}
	return c.Status()
}

func (c *TrackingController) Status() TrackingStatus {
	if c == nil {
		return TrackingStatus{MotionState: MotionUnknown}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.statusCopyLocked()
}

func (c *TrackingController) statusCopyLocked() TrackingStatus {
	result := c.status
	result.LastRequestAt, result.LastResponseAt, result.NextRequestAt = copyTime(c.status.LastRequestAt), copyTime(c.status.LastResponseAt), copyTime(c.status.NextRequestAt)
	result.LastPathUpdateAt = copyTime(c.status.LastPathUpdateAt)
	result.LastTelemetryRequestTag = copyUint32(c.status.LastTelemetryRequestTag)
	result.LastTelemetryResponseTag = copyUint32(c.status.LastTelemetryResponseTag)
	result.LastTelemetryExpiredTag = copyUint32(c.status.LastTelemetryExpiredTag)
	if c.status.EstimatedSpeedKmh != nil {
		value := *c.status.EstimatedSpeedKmh
		result.EstimatedSpeedKmh = &value
	}
	if c.status.LastError != nil {
		value := *c.status.LastError
		result.LastError = &value
	}
	if c.status.LatestTelemetry != nil {
		value := copyTelemetryResult(*c.status.LatestTelemetry)
		result.LatestTelemetry = &value
	}
	result.RecentPolls = make([]TrackingPoll, len(c.status.RecentPolls))
	for i, poll := range c.status.RecentPolls {
		result.RecentPolls[i] = copyTrackingPoll(poll)
	}
	return result
}

func copyTelemetryResult(result TelemetryResult) TelemetryResult {
	result.RawFrame = nil
	result.Telemetry.UnsupportedTypes = append([]int(nil), result.Telemetry.UnsupportedTypes...)
	result.Telemetry.Voltage = copyFloat64(result.Telemetry.Voltage)
	result.Telemetry.Latitude = copyFloat64(result.Telemetry.Latitude)
	result.Telemetry.Longitude = copyFloat64(result.Telemetry.Longitude)
	result.Telemetry.AltitudeM = copyFloat64(result.Telemetry.AltitudeM)
	result.Telemetry.TemperatureC = copyFloat64(result.Telemetry.TemperatureC)
	if result.Telemetry.BatteryPercentage != nil {
		value := *result.Telemetry.BatteryPercentage
		result.Telemetry.BatteryPercentage = &value
	}
	result.RouteAttempt = result.RouteAttempt.copy()
	result.RFEvidence = copyRFEvidence(result.RFEvidence)
	return result
}

func copyTrackingPoll(poll TrackingPoll) TrackingPoll {
	poll.ResponseAt = copyTime(poll.ResponseAt)
	poll.RouteAttempt = poll.RouteAttempt.copy()
	if poll.ResponseRoute != nil {
		value := poll.ResponseRoute.copy()
		poll.ResponseRoute = &value
	}
	if poll.EstimatedSpeedKmh != nil {
		value := *poll.EstimatedSpeedKmh
		poll.EstimatedSpeedKmh = &value
	}
	if poll.Error != nil {
		value := *poll.Error
		poll.Error = &value
	}
	poll.RequestTag = copyUint32(poll.RequestTag)
	poll.ResponseTag = copyUint32(poll.ResponseTag)
	poll.RFEvidence = copyRFEvidence(poll.RFEvidence)
	return poll
}

func (c *TrackingController) run(ctx context.Context, target string, generation uint64) {
	for {
		requestAt, tagged := c.poll(ctx, target, generation)
		c.mu.RLock()
		if !c.currentLocked(target, generation) {
			c.mu.RUnlock()
			return
		}
		delay := time.Duration(c.status.CurrentIntervalSeconds) * time.Second
		// A tagged binary request can be safely expired without a later 0x8C
		// satisfying its replacement. For Session-owned discovery, schedule from
		// the actual request start rather than response/timeout completion.
		// Legacy prefix-only telemetry intentionally retains its conservative
		// completion-based behavior.
		next := nextTrackingRequestAt(c.now().UTC(), requestAt, delay, c.controlSource == "session", tagged)
		c.mu.RUnlock()
		c.mu.Lock()
		if c.currentLocked(target, generation) {
			c.status.NextRequestAt = timePtr(next)
		}
		c.mu.Unlock()
		waitFor := next.Sub(c.now().UTC())
		if waitFor < 0 {
			waitFor = 0
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-c.scheduleChanged:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		case <-timer.C:
		}
	}
}

func nextTrackingRequestAt(now, requestAt time.Time, interval time.Duration, sessionManaged, tagged bool) time.Time {
	now = now.UTC()
	if !sessionManaged || !tagged || requestAt.IsZero() {
		return now.Add(interval)
	}
	next := requestAt.UTC().Add(interval)
	if next.Before(now) {
		return now
	}
	return next
}

func (c *TrackingController) poll(ctx context.Context, target string, generation uint64) (time.Time, bool) {
	now := c.now().UTC()
	c.mu.Lock()
	if !c.currentLocked(target, generation) {
		c.mu.Unlock()
		return time.Time{}, false
	}
	c.status.LastRequestAt, c.status.NextRequestAt = timePtr(now), nil
	c.mu.Unlock()
	c.logger.Info("MeshCore tracking poll initiated", "target_public_key", target)
	result, err := c.request(context.Background(), target)
	requestAt := now
	if !result.RequestedAt.IsZero() {
		requestAt = result.RequestedAt.UTC()
	}
	if err != nil {
		c.recoverStaleRouteTimeout(ctx, target, generation, result, err)
		c.recordFailure(target, generation, result, err, requestAt)
		return requestAt, result.Tagged
	}
	result = c.withSuccessfulRouteEvidence(target, generation, result)
	c.mu.RLock()
	stage := c.stageSuccessfulTelemetry
	c.mu.RUnlock()
	if stage != nil {
		if err := stage(result); err != nil {
			c.recordFailure(target, generation, result, err, requestAt)
			return requestAt, result.Tagged
		}
	}
	c.recordSuccess(target, generation, result, requestAt)
	return requestAt, result.Tagged
}

// withSuccessfulRouteEvidence snapshots only the request-side tracking facts
// relevant to this response. recordSuccess consumes the same pending state
// after staging; this preview must not mutate controller state.
func (c *TrackingController) withSuccessfulRouteEvidence(target string, generation uint64, result TelemetryResult) TelemetryResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.currentLocked(target, generation) {
		return result
	}
	if c.pendingRecoveryEvent != "" {
		result.RouteRecovery = c.pendingRecoveryEvent
	} else if c.recoveryEpisodeActive {
		switch result.RouteAttempt.Mode {
		case RouteModeFlood:
			result.RouteRecovery = "flood_attempted"
		case RouteModeExplicitPath:
			result.RouteRecovery = "explicit_path_observed"
		case RouteModeZeroHop:
			result.RouteRecovery = "zero_hop_observed"
		}
	}
	result.PathUpdateObserved = c.status.PathUpdatePending
	return result
}

func (c *TrackingController) recoverStaleRouteTimeout(ctx context.Context, target string, generation uint64, result TelemetryResult, err error) string {
	c.mu.Lock()
	if !c.currentLocked(target, generation) {
		c.mu.Unlock()
		return ""
	}
	// The contact snapshot is only an observation. Reset a Companion-owned
	// path only after RESP_CODE_SENT confirmed the same request was actually
	// sent as direct, and only after a short same-route timeout streak.
	if !errors.Is(err, ErrTelemetryTimeout) || !staleRouteCandidate(result.RouteAttempt) || c.reset == nil {
		c.clearStaleRouteFailuresLocked()
		c.mu.Unlock()
		return ""
	}
	c.noteStaleRouteFailureLocked(result.RouteAttempt)
	if c.staleRouteFailures < staleRouteFailureThreshold || ctx.Err() != nil {
		c.mu.Unlock()
		return ""
	}
	if c.recoveryEpisodeActive {
		// An acknowledged reset already changed Companion state; wait for the
		// resulting normal flood/path-learning traffic rather than sending more
		// resets. A failed command changed nothing, so retry only after another
		// complete same-route streak (3, 6, 9...) to avoid a command loop.
		if c.pathResetAcknowledged || c.staleRouteFailures%staleRouteFailureThreshold != 0 {
			c.mu.Unlock()
			return ""
		}
	} else {
		c.recoveryEpisodeActive = true
		c.status.PathResetAttempts = 0
	}
	c.status.RouteRecoveryState = "path_reset_requested"
	c.status.RecoveryEpisodeActive = true
	c.status.LastRouteRecoveryEvent = "path_reset_requested"
	c.status.PathResetAttempts++
	staleFailures := c.staleRouteFailures
	c.mu.Unlock()
	c.logger.Warn("MeshCore tracking stale-route timeout threshold reached; requesting path reset", "target_public_key", target, "route_mode", result.RouteAttempt.Mode, "path_length", result.RouteAttempt.PathLength, "stale_route_failures", staleFailures, "stale_route_failure_threshold", staleRouteFailureThreshold)
	if err := c.reset(ctx, target); err != nil {
		c.mu.Lock()
		if c.currentLocked(target, generation) {
			c.status.RouteRecoveryState = "recovery_active"
			c.status.LastRouteRecoveryEvent = "path_reset_failed"
			c.pendingRecoveryEvent = "path_reset_failed"
		}
		c.mu.Unlock()
		c.logger.Warn("MeshCore tracking path reset failed", "target_public_key", target, "err", err)
		return "path_reset_failed"
	}
	c.mu.Lock()
	if c.currentLocked(target, generation) {
		c.status.RouteRecoveryState = "recovery_active"
		c.status.LastRouteRecoveryEvent = "path_reset_acknowledged"
		c.pathResetAcknowledged = true
		c.pendingRecoveryEvent = "path_reset_acknowledged"
	}
	c.mu.Unlock()
	c.logger.Info("MeshCore tracking path reset acknowledged", "target_public_key", target)
	return "path_reset_acknowledged"
}

func (c *TrackingController) recordSuccess(target string, generation uint64, result TelemetryResult, requestTimes ...time.Time) {
	requestAt := result.ReceivedAt.UTC()
	if len(requestTimes) > 0 {
		requestAt = requestTimes[0].UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.currentLocked(target, generation) {
		return
	}
	wasUnavailable := c.status.LastError != nil && *c.status.LastError == ErrTelemetryAdapterDisconnected.Error()
	previousInterval := c.status.CurrentIntervalSeconds
	c.status.LastResponseAt = timePtr(result.ReceivedAt.UTC())
	latest := copyTelemetryResult(result)
	c.status.LatestTelemetry = &latest
	c.status.ConsecutiveFailures, c.status.LastError = 0, nil
	c.clearStaleRouteFailuresLocked()
	c.recordTelemetryDiagnosticsLocked(result, nil)
	c.applyFixLocked(result)
	c.status.CurrentIntervalSeconds = int64(c.intervalForLocked() / time.Second)
	recovery := c.routeRecoveryForPollLocked(result.RouteAttempt)
	c.establishRouteLocked(result.RouteAttempt)
	pathUpdateObserved := c.status.PathUpdatePending
	c.status.PathUpdatePending = false
	c.appendPollLocked(TrackingPoll{
		RequestAt: requestAt, ResponseAt: timePtr(result.ReceivedAt), Outcome: "success",
		EstimatedSpeedKmh: copyFloat64(c.status.EstimatedSpeedKmh), MotionState: c.status.MotionState,
		IntervalSeconds: c.status.CurrentIntervalSeconds, RouteAttempt: routeEvidenceOrUnknown(result.RouteAttempt),
		ResponseRouteUnknown: true, RouteRecovery: recovery, PathUpdateObserved: pathUpdateObserved, ConsecutiveFailures: c.status.ConsecutiveFailures,
		StaleRouteFailures: c.status.StaleRouteFailures,
		TelemetryTransport: result.Transport, RequestTag: copyUint32(result.RequestTag), ResponseTag: copyUint32(result.ResponseTag), FallbackReason: result.FallbackReason,
		RFEvidence: copyRFEvidence(result.RFEvidence),
	})
	c.logger.Info("MeshCore tracking poll succeeded", "target_public_key", target,
		"elapsed", result.ReceivedAt.Sub(requestAt).String(), "motion_state", c.status.MotionState,
		"estimated_speed_kmh", c.status.EstimatedSpeedKmh, "route_mode", routeEvidenceOrUnknown(result.RouteAttempt).Mode,
		"path_length", routeEvidenceOrUnknown(result.RouteAttempt).PathLength, "route_source", routeEvidenceOrUnknown(result.RouteAttempt).Source,
		"response_route_unknown", true)
	if c.status.CurrentIntervalSeconds != previousInterval {
		c.logger.Info("MeshCore tracking interval changed", "target_public_key", target, "interval_seconds", c.status.CurrentIntervalSeconds)
	}
	if wasUnavailable {
		c.logger.Info("MeshCore tracking adapter recovered", "target_public_key", target)
	}
}

func (c *TrackingController) recordFailure(target string, generation uint64, result TelemetryResult, err error, requestTimes ...time.Time) {
	requestAt := c.now().UTC()
	if len(requestTimes) > 0 {
		requestAt = requestTimes[0].UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.currentLocked(target, generation) {
		return
	}
	wasUnavailable := c.status.LastError != nil && *c.status.LastError == ErrTelemetryAdapterDisconnected.Error()
	c.status.ConsecutiveFailures++
	message := err.Error()
	c.status.LastError = &message
	c.recordTelemetryDiagnosticsLocked(result, err)
	next, scheduleSource := c.failureIntervalLocked(err)
	c.status.CurrentIntervalSeconds = int64(next / time.Second)
	c.status.LastFailureScheduleSource = scheduleSource
	recovery := c.routeRecoveryForPollLocked(result.RouteAttempt)
	pathUpdateObserved := c.status.PathUpdatePending
	c.status.PathUpdatePending = false
	c.appendPollLocked(TrackingPoll{
		RequestAt: requestAt, Outcome: trackingOutcome(err), Error: stringPtr(message),
		EstimatedSpeedKmh: copyFloat64(c.status.EstimatedSpeedKmh), MotionState: c.status.MotionState,
		IntervalSeconds: c.status.CurrentIntervalSeconds, RouteAttempt: routeEvidenceOrUnknown(result.RouteAttempt),
		ResponseRouteUnknown: true, RouteRecovery: recovery, PathUpdateObserved: pathUpdateObserved, ConsecutiveFailures: c.status.ConsecutiveFailures,
		StaleRouteFailures: c.status.StaleRouteFailures,
		TelemetryTransport: result.Transport, RequestTag: copyUint32(result.RequestTag), ResponseTag: copyUint32(result.ResponseTag), FallbackReason: result.FallbackReason, ScheduleSource: scheduleSource,
		RFEvidence: copyRFEvidence(result.RFEvidence),
	})
	c.logger.Warn("MeshCore tracking poll timed out", "target_public_key", target, "err", err,
		"route_mode", routeEvidenceOrUnknown(result.RouteAttempt).Mode, "path_length", routeEvidenceOrUnknown(result.RouteAttempt).PathLength,
		"route_source", routeEvidenceOrUnknown(result.RouteAttempt).Source, "consecutive_failures", c.status.ConsecutiveFailures,
		"next_interval", next.String(), "schedule_source", scheduleSource, "control_source", c.controlSource)
	c.logger.Info("MeshCore tracking failure schedule changed", "target_public_key", target, "interval", next.String(), "schedule_source", scheduleSource, "control_source", c.controlSource)
	if errors.Is(err, ErrTelemetryAdapterDisconnected) && !wasUnavailable {
		c.logger.Warn("MeshCore tracking adapter unavailable", "target_public_key", target)
	}
}

func (c *TrackingController) recordTelemetryDiagnosticsLocked(result TelemetryResult, err error) {
	if result.Transport != "" {
		c.status.TelemetryTransport = result.Transport
	}
	if result.Capability != "" {
		c.status.TelemetryCapability = result.Capability
	}
	if result.FallbackReason != "" {
		c.status.LastTelemetryFallbackReason = result.FallbackReason
	}
	if result.RequestTag != nil {
		c.status.LastTelemetryRequestTag = copyUint32(result.RequestTag)
	}
	if result.ResponseTag != nil {
		c.status.LastTelemetryResponseTag = copyUint32(result.ResponseTag)
	}
	if errors.Is(err, ErrTelemetryTimeout) && result.RequestTag != nil {
		c.status.LastTelemetryExpiredTag = copyUint32(result.RequestTag)
	}
}

// failureIntervalLocked distinguishes a remote-contact failure from local
// transport loss. An explicitly started Session keeps searching for RF
// coverage at its last valid motion cadence; the BLE adapter's own bounded
// reconnect behavior is protected by the existing exponential backoff.
func (c *TrackingController) failureIntervalLocked(err error) (time.Duration, string) {
	if c.controlSource == "session" && !errors.Is(err, ErrTelemetryAdapterDisconnected) {
		return c.intervalForLocked(), "motion"
	}
	next := time.Duration(c.status.CurrentIntervalSeconds) * time.Second
	if c.status.ConsecutiveFailures == 1 {
		next = maxDuration(c.policy.UnknownInterval, next)
	} else {
		next *= 2
	}
	if next > c.policy.MaximumBackoff {
		next = c.policy.MaximumBackoff
	}
	next = maxDuration(next, c.policy.MinimumInterval)
	if errors.Is(err, ErrTelemetryAdapterDisconnected) {
		return next, "adapter_recovery"
	}
	return next, "manual_backoff"
}

func recoverableRoute(evidence RouteEvidence) bool {
	return evidence.Mode == RouteModeZeroHop || evidence.Mode == RouteModeExplicitPath
}

// staleRouteCandidate requires both the cached-contact snapshot and the
// firmware's direct-send acknowledgement. A contact path alone does not prove
// that the telemetry command reached the send path, so it must not trigger a
// contact mutation.
func staleRouteCandidate(evidence RouteEvidence) bool {
	return recoverableRoute(evidence) && evidence.Source == "contact_out_path+response_sent"
}

func routeFingerprint(evidence RouteEvidence) string {
	if !recoverableRoute(evidence) && evidence.Mode != RouteModeFlood {
		return ""
	}
	return string(evidence.Mode) + ":" + strings.Join(evidence.Path, ",")
}

func (c *TrackingController) noteStaleRouteFailureLocked(evidence RouteEvidence) {
	fingerprint := routeFingerprint(evidence)
	if fingerprint == "" {
		c.clearStaleRouteFailuresLocked()
		return
	}
	if c.staleRouteFingerprint != fingerprint {
		c.staleRouteFingerprint, c.staleRouteFailures = fingerprint, 1
	} else {
		c.staleRouteFailures++
	}
	c.status.StaleRouteFailures = c.staleRouteFailures
	c.status.StaleRouteFailureThreshold = staleRouteFailureThreshold
}

func (c *TrackingController) clearStaleRouteFailuresLocked() {
	c.staleRouteFingerprint, c.staleRouteFailures = "", 0
	c.status.StaleRouteFailures = 0
	c.status.StaleRouteFailureThreshold = staleRouteFailureThreshold
}

// routeRecoveryForPollLocked creates history evidence only for an actual
// recovery action or actual route mode. It never carries an old flood label
// into a later zero-hop poll.
func (c *TrackingController) routeRecoveryForPollLocked(evidence RouteEvidence) string {
	if c.pendingRecoveryEvent != "" {
		event := c.pendingRecoveryEvent
		c.pendingRecoveryEvent = ""
		return event
	}
	if !c.recoveryEpisodeActive {
		return ""
	}
	switch evidence.Mode {
	case RouteModeFlood:
		return "flood_attempted"
	case RouteModeExplicitPath:
		return "explicit_path_observed"
	case RouteModeZeroHop:
		return "zero_hop_observed"
	default:
		return ""
	}
}

// establishRouteLocked closes a recovery episode only after a successful
// actual telemetry response. A changed zero-hop or explicit route becomes a
// new generation that can later have its own stale-route recovery episode.
func (c *TrackingController) establishRouteLocked(evidence RouteEvidence) {
	fingerprint := routeFingerprint(evidence)
	if fingerprint == "" {
		return
	}
	if fingerprint != c.routeFingerprint {
		c.routeFingerprint = fingerprint
		c.routeGeneration++
		c.status.RouteFingerprint = fingerprint
		c.status.RouteGeneration = c.routeGeneration
	}
	if c.recoveryEpisodeActive {
		c.recoveryEpisodeActive = false
		c.pathResetAcknowledged = false
		c.status.RecoveryEpisodeActive = false
		c.status.RouteRecoveryState = "recovery_complete"
		c.status.LastRouteRecoveryEvent = "route_recovered"
		c.logger.Info("MeshCore tracking route recovery episode completed", "route_generation", c.routeGeneration, "route_mode", evidence.Mode, "path_length", evidence.PathLength)
	}
}

// HandlePathUpdated records the Companion's key-only path-update notice. The
// next normal telemetry poll obtains a fresh contact snapshot; no repeater is
// selected or inferred locally.
func (c *TrackingController) HandlePathUpdated(publicKey string, observedAt time.Time) {
	if c == nil || observedAt.IsZero() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.status.Active || c.status.TargetPublicKey != publicKey {
		return
	}
	c.status.LastPathUpdateAt = timePtr(observedAt)
	c.status.PathUpdatePending = true
	c.logger.Info("MeshCore tracking path update received; contact route will refresh on next poll", "target_public_key", publicKey)
}

func (c *TrackingController) appendPollLocked(poll TrackingPoll) {
	c.status.RecentPolls = append(c.status.RecentPolls, copyTrackingPoll(poll))
	if len(c.status.RecentPolls) > recentPollLimit {
		c.status.RecentPolls = append([]TrackingPoll(nil), c.status.RecentPolls[len(c.status.RecentPolls)-recentPollLimit:]...)
	}
}

func trackingOutcome(err error) string {
	if errors.Is(err, ErrTelemetryTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

func copyFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyUint32(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyRFEvidence(value RFEvidence) RFEvidence {
	value.DeltaMS = int64PtrValue(value.DeltaMS)
	value.FrameSequenceDelta = uint64PtrValue(value.FrameSequenceDelta)
	value.CandidateSequence = uint64PtrValue(value.CandidateSequence)
	value.RSSI = intPtrValue(value.RSSI)
	value.SNR = copyFloat64(value.SNR)
	value.NearestCandidateSequence = uint64PtrValue(value.NearestCandidateSequence)
	value.NearestCandidateDeltaMS = int64PtrValue(value.NearestCandidateDeltaMS)
	value.NearestCandidateFrameSequenceDelta = uint64PtrValue(value.NearestCandidateFrameSequenceDelta)
	value.NearestCandidateRSSI = intPtrValue(value.NearestCandidateRSSI)
	value.NearestCandidateSNR = copyFloat64(value.NearestCandidateSNR)
	value.NearestCandidateIsImmediatePredecessor = boolPtrValue(value.NearestCandidateIsImmediatePredecessor)
	return value
}

func int64PtrValue(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func uint64PtrValue(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func intPtrValue(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func boolPtrValue(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func routeEvidenceOrUnknown(evidence RouteEvidence) RouteEvidence {
	if evidence.Mode == "" {
		return unknownRouteEvidence("not_available")
	}
	return evidence.copy()
}

func stringPtr(value string) *string { return &value }

func (c *TrackingController) applyFixLocked(result TelemetryResult) {
	lat, lon := result.Telemetry.Latitude, result.Telemetry.Longitude
	if lat == nil || lon == nil {
		c.setOperationalErrorLocked("telemetry response has no usable GPS")
		return
	}
	if !validCoordinate(*lat, *lon) {
		c.setOperationalErrorLocked("telemetry response GPS is invalid")
		return
	}
	observedAt := result.ReceivedAt.UTC()
	if observedAt.IsZero() {
		c.setOperationalErrorLocked("telemetry response timestamp is invalid")
		return
	}
	current := trackingFix{latitude: *lat, longitude: *lon, observedAt: observedAt}
	if c.fix == nil {
		c.fix = &current
		return
	}
	elapsed := current.observedAt.Sub(c.fix.observedAt)
	if elapsed < c.policy.MinimumSampleAge {
		c.fix = &current
		c.setOperationalErrorLocked("telemetry GPS samples are too close together")
		return
	}
	if elapsed <= 0 {
		c.setOperationalErrorLocked("telemetry GPS sample timestamp did not advance")
		return
	}
	distance := haversineMeters(c.fix.latitude, c.fix.longitude, current.latitude, current.longitude)
	if math.IsNaN(distance) || math.IsInf(distance, 0) || distance > c.policy.MaximumJumpMeters {
		c.setOperationalErrorLocked("telemetry GPS jump ignored")
		return
	}
	speed := distance / elapsed.Hours() / 1000
	if math.IsNaN(speed) || math.IsInf(speed, 0) || speed > c.policy.MaximumSpeedKmh {
		c.setOperationalErrorLocked("telemetry GPS speed is implausible")
		return
	}
	c.fix = &current
	c.status.EstimatedSpeedKmh = &speed
	c.status.LastError = nil
	c.applyMotionLocked(c.classify(speed))
}

func (c *TrackingController) applyMotionLocked(candidate MotionState) {
	if c.status.MotionState == candidate {
		c.pending, c.pendingCount = MotionUnknown, 0
		return
	}
	if c.status.MotionState == MotionUnknown {
		c.setMotionLocked(candidate)
		return
	}
	if c.pending != candidate {
		c.pending, c.pendingCount = candidate, 1
		return
	}
	c.pendingCount++
	if c.pendingCount >= 2 {
		c.setMotionLocked(candidate)
	}
}

func (c *TrackingController) setMotionLocked(next MotionState) {
	old := c.status.MotionState
	c.status.MotionState, c.pending, c.pendingCount = next, MotionUnknown, 0
	if old != next {
		c.logger.Info("MeshCore tracking motion classification changed", "from", old, "to", next)
	}
}

func (c *TrackingController) setOperationalErrorLocked(message string) { c.status.LastError = &message }
func (c *TrackingController) intervalForLocked() time.Duration {
	switch c.status.MotionState {
	case MotionStationary:
		return c.policy.StationaryInterval
	case MotionSlow:
		return c.policy.SlowInterval
	case MotionFast:
		return c.policy.FastInterval
	default:
		return c.policy.UnknownInterval
	}
}
func (c *TrackingController) currentLocked(target string, generation uint64) bool {
	return c.status.Active && c.status.TargetPublicKey == target && c.generation == generation
}
func (c *TrackingController) classify(speed float64) MotionState {
	if speed < c.policy.StationaryKmh {
		return MotionStationary
	}
	if speed <= c.policy.SlowKmh {
		return MotionSlow
	}
	return MotionFast
}
func validCoordinate(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsNaN(longitude) && !math.IsInf(latitude, 0) && !math.IsInf(longitude, 0) && latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}
func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	radians := math.Pi / 180
	dLat, dLon := (lat2-lat1)*radians, (lon2-lon1)*radians
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*radians)*math.Cos(lat2*radians)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return r * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
func timePtr(value time.Time) *time.Time { value = value.UTC(); return &value }
func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
