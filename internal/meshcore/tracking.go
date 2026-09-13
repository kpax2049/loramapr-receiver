package meshcore

import (
	"context"
	"errors"
	"log/slog"
	"math"
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
	Active                 bool           `json:"active"`
	TargetPublicKey        string         `json:"targetPublicKey,omitempty"`
	MotionState            MotionState    `json:"motionState"`
	EstimatedSpeedKmh      *float64       `json:"estimatedSpeedKmh"`
	CurrentIntervalSeconds int64          `json:"currentIntervalSeconds"`
	LastRequestAt          *time.Time     `json:"lastRequestAt"`
	LastResponseAt         *time.Time     `json:"lastResponseAt"`
	NextRequestAt          *time.Time     `json:"nextRequestAt"`
	ConsecutiveFailures    int            `json:"consecutiveFailures"`
	LastError              *string        `json:"lastError"`
	RouteRecoveryState     string         `json:"routeRecoveryState"`
	LastPathUpdateAt       *time.Time     `json:"lastPathUpdateAt"`
	PathUpdatePending      bool           `json:"pathUpdatePending"`
	RecentPolls            []TrackingPoll `json:"recentPolls"`
}

const recentPollLimit = 30

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
	PathUpdateObserved   bool           `json:"pathUpdateObserved"`
	ConsecutiveFailures  int            `json:"consecutiveFailures"`
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
	request telemetryRequester
	reset   pathResetter
	policy  TrackingPolicy
	logger  *slog.Logger
	now     func() time.Time

	mu                    sync.RWMutex
	status                TrackingStatus
	fix                   *trackingFix
	pending               MotionState
	pendingCount          int
	cancel                context.CancelFunc
	generation            uint64
	zeroHopResetAttempted bool
}

func NewTrackingController(request func(context.Context, string) (TelemetryResult, error), policy TrackingPolicy, logger *slog.Logger) *TrackingController {
	return NewTrackingControllerWithRouteRecovery(request, nil, policy, logger)
}

// NewTrackingControllerWithRouteRecovery extends the local tracking harness
// with the single source-supported recovery action: reset a stale zero-hop
// contact path and let Companion choose flood/path learning itself.
func NewTrackingControllerWithRouteRecovery(request func(context.Context, string) (TelemetryResult, error), reset func(context.Context, string) error, policy TrackingPolicy, logger *slog.Logger) *TrackingController {
	if logger == nil {
		logger = slog.Default()
	}
	p := policy.normalized()
	return &TrackingController{request: request, reset: reset, policy: p, logger: logger.With("component", "meshcore_tracking"), now: func() time.Time { return time.Now().UTC() }, status: TrackingStatus{MotionState: MotionUnknown, CurrentIntervalSeconds: int64(p.UnknownInterval / time.Second), RecentPolls: []TrackingPoll{}}}
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
	c.fix, c.pending, c.pendingCount, c.zeroHopResetAttempted = nil, MotionUnknown, 0, false
	c.status = TrackingStatus{Active: true, TargetPublicKey: publicKey, MotionState: MotionUnknown, CurrentIntervalSeconds: int64(c.policy.UnknownInterval / time.Second), RecentPolls: []TrackingPoll{}}
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
	if c.status.EstimatedSpeedKmh != nil {
		value := *c.status.EstimatedSpeedKmh
		result.EstimatedSpeedKmh = &value
	}
	if c.status.LastError != nil {
		value := *c.status.LastError
		result.LastError = &value
	}
	result.RecentPolls = make([]TrackingPoll, len(c.status.RecentPolls))
	for i, poll := range c.status.RecentPolls {
		result.RecentPolls[i] = copyTrackingPoll(poll)
	}
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
	return poll
}

func (c *TrackingController) run(ctx context.Context, target string, generation uint64) {
	for {
		c.poll(ctx, target, generation)
		c.mu.RLock()
		if !c.currentLocked(target, generation) {
			c.mu.RUnlock()
			return
		}
		delay := time.Duration(c.status.CurrentIntervalSeconds) * time.Second
		next := c.now().UTC().Add(delay)
		c.mu.RUnlock()
		c.mu.Lock()
		if c.currentLocked(target, generation) {
			c.status.NextRequestAt = timePtr(next)
		}
		c.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (c *TrackingController) poll(ctx context.Context, target string, generation uint64) {
	now := c.now().UTC()
	c.mu.Lock()
	if !c.currentLocked(target, generation) {
		c.mu.Unlock()
		return
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
		c.recoverZeroHopTimeout(ctx, target, generation, result, err)
		c.recordFailure(target, generation, result, err, requestAt)
		return
	}
	c.recordSuccess(target, generation, result, requestAt)
}

func (c *TrackingController) recoverZeroHopTimeout(ctx context.Context, target string, generation uint64, result TelemetryResult, err error) string {
	if !errors.Is(err, ErrTelemetryTimeout) || result.RouteAttempt.Mode != RouteModeZeroHop || c.reset == nil {
		return ""
	}
	c.mu.Lock()
	if !c.currentLocked(target, generation) || c.zeroHopResetAttempted || ctx.Err() != nil {
		c.mu.Unlock()
		return ""
	}
	c.zeroHopResetAttempted = true
	c.status.RouteRecoveryState = "path_reset_requested"
	c.mu.Unlock()
	c.logger.Warn("MeshCore tracking zero-hop telemetry timed out; requesting path reset", "target_public_key", target)
	if err := c.reset(ctx, target); err != nil {
		c.mu.Lock()
		if c.currentLocked(target, generation) {
			c.status.RouteRecoveryState = "path_reset_failed"
		}
		c.mu.Unlock()
		c.logger.Warn("MeshCore tracking path reset failed", "target_public_key", target, "err", err)
		return "path_reset_failed"
	}
	c.mu.Lock()
	if c.currentLocked(target, generation) {
		c.status.RouteRecoveryState = "path_reset_acknowledged"
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
	c.status.ConsecutiveFailures, c.status.LastError = 0, nil
	c.applyFixLocked(result)
	c.status.CurrentIntervalSeconds = int64(c.intervalForLocked() / time.Second)
	if result.RouteAttempt.Mode == RouteModeFlood && c.status.RouteRecoveryState == "path_reset_acknowledged" {
		c.status.RouteRecoveryState = "flood_attempted"
		c.logger.Info("MeshCore tracking poll used flood after path reset", "target_public_key", target)
	} else if result.RouteAttempt.Mode == RouteModeExplicitPath && (c.status.RouteRecoveryState == "path_reset_acknowledged" || c.status.RouteRecoveryState == "flood_attempted") {
		c.status.RouteRecoveryState = "explicit_path_observed"
		c.logger.Info("MeshCore tracking explicit path observed after route recovery", "target_public_key", target, "path_length", result.RouteAttempt.PathLength)
	}
	pathUpdateObserved := c.status.PathUpdatePending
	c.status.PathUpdatePending = false
	c.appendPollLocked(TrackingPoll{
		RequestAt: requestAt, ResponseAt: timePtr(result.ReceivedAt), Outcome: "success",
		EstimatedSpeedKmh: copyFloat64(c.status.EstimatedSpeedKmh), MotionState: c.status.MotionState,
		IntervalSeconds: c.status.CurrentIntervalSeconds, RouteAttempt: routeEvidenceOrUnknown(result.RouteAttempt),
		ResponseRouteUnknown: true, RouteRecovery: c.status.RouteRecoveryState, PathUpdateObserved: pathUpdateObserved, ConsecutiveFailures: c.status.ConsecutiveFailures,
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
	previous := time.Duration(c.status.CurrentIntervalSeconds) * time.Second
	if c.status.ConsecutiveFailures == 1 {
		previous = maxDuration(c.policy.UnknownInterval, previous)
	} else {
		previous *= 2
	}
	if previous > c.policy.MaximumBackoff {
		previous = c.policy.MaximumBackoff
	}
	previous = maxDuration(previous, c.policy.MinimumInterval)
	c.status.CurrentIntervalSeconds = int64(previous / time.Second)
	recovery := c.status.RouteRecoveryState
	pathUpdateObserved := c.status.PathUpdatePending
	c.status.PathUpdatePending = false
	c.appendPollLocked(TrackingPoll{
		RequestAt: requestAt, Outcome: trackingOutcome(err), Error: stringPtr(message),
		EstimatedSpeedKmh: copyFloat64(c.status.EstimatedSpeedKmh), MotionState: c.status.MotionState,
		IntervalSeconds: c.status.CurrentIntervalSeconds, RouteAttempt: routeEvidenceOrUnknown(result.RouteAttempt),
		ResponseRouteUnknown: true, RouteRecovery: recovery, PathUpdateObserved: pathUpdateObserved, ConsecutiveFailures: c.status.ConsecutiveFailures,
	})
	c.logger.Warn("MeshCore tracking poll timed out", "target_public_key", target, "err", err,
		"route_mode", routeEvidenceOrUnknown(result.RouteAttempt).Mode, "path_length", routeEvidenceOrUnknown(result.RouteAttempt).PathLength,
		"route_source", routeEvidenceOrUnknown(result.RouteAttempt).Source, "consecutive_failures", c.status.ConsecutiveFailures,
		"next_backoff", previous.String())
	c.logger.Info("MeshCore tracking backoff changed", "target_public_key", target, "interval", previous.String())
	if errors.Is(err, ErrTelemetryAdapterDisconnected) && !wasUnavailable {
		c.logger.Warn("MeshCore tracking adapter unavailable", "target_public_key", target)
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
