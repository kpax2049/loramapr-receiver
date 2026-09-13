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
		UnknownInterval: 30 * time.Second, StationaryInterval: 120 * time.Second,
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
	Active                 bool        `json:"active"`
	TargetPublicKey        string      `json:"targetPublicKey,omitempty"`
	MotionState            MotionState `json:"motionState"`
	EstimatedSpeedKmh      *float64    `json:"estimatedSpeedKmh"`
	CurrentIntervalSeconds int64       `json:"currentIntervalSeconds"`
	LastRequestAt          *time.Time  `json:"lastRequestAt"`
	LastResponseAt         *time.Time  `json:"lastResponseAt"`
	NextRequestAt          *time.Time  `json:"nextRequestAt"`
	ConsecutiveFailures    int         `json:"consecutiveFailures"`
	LastError              *string     `json:"lastError"`
}

type telemetryRequester func(context.Context, string) (TelemetryResult, error)

type trackingFix struct {
	latitude, longitude float64
	observedAt          time.Time
}

// TrackingController schedules correlated telemetry requests through the
// existing request path. It intentionally owns no radio transport or durable
// state, which keeps it ready to be lifecycle-owned by a cloud Session.
type TrackingController struct {
	request telemetryRequester
	policy  TrackingPolicy
	logger  *slog.Logger
	now     func() time.Time

	mu           sync.RWMutex
	status       TrackingStatus
	fix          *trackingFix
	pending      MotionState
	pendingCount int
	cancel       context.CancelFunc
	generation   uint64
}

func NewTrackingController(request func(context.Context, string) (TelemetryResult, error), policy TrackingPolicy, logger *slog.Logger) *TrackingController {
	if logger == nil {
		logger = slog.Default()
	}
	p := policy.normalized()
	return &TrackingController{request: request, policy: p, logger: logger.With("component", "meshcore_tracking"), now: func() time.Time { return time.Now().UTC() }, status: TrackingStatus{MotionState: MotionUnknown, CurrentIntervalSeconds: int64(p.UnknownInterval / time.Second)}}
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
	c.status = TrackingStatus{Active: true, TargetPublicKey: publicKey, MotionState: MotionUnknown, CurrentIntervalSeconds: int64(c.policy.UnknownInterval / time.Second)}
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
	if c.status.EstimatedSpeedKmh != nil {
		value := *c.status.EstimatedSpeedKmh
		result.EstimatedSpeedKmh = &value
	}
	if c.status.LastError != nil {
		value := *c.status.LastError
		result.LastError = &value
	}
	return result
}

func (c *TrackingController) run(ctx context.Context, target string, generation uint64) {
	for {
		c.poll(target, generation)
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

func (c *TrackingController) poll(target string, generation uint64) {
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
	if err != nil {
		c.recordFailure(target, generation, err)
		return
	}
	c.recordSuccess(target, generation, result)
}

func (c *TrackingController) recordSuccess(target string, generation uint64, result TelemetryResult) {
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
	c.logger.Info("MeshCore tracking poll succeeded", "target_public_key", target)
	if c.status.CurrentIntervalSeconds != previousInterval {
		c.logger.Info("MeshCore tracking interval changed", "target_public_key", target, "interval_seconds", c.status.CurrentIntervalSeconds)
	}
	if wasUnavailable {
		c.logger.Info("MeshCore tracking adapter recovered", "target_public_key", target)
	}
}

func (c *TrackingController) recordFailure(target string, generation uint64, err error) {
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
	c.logger.Warn("MeshCore tracking poll failed", "target_public_key", target, "err", err, "consecutive_failures", c.status.ConsecutiveFailures)
	c.logger.Info("MeshCore tracking backoff changed", "target_public_key", target, "interval", previous.String())
	if errors.Is(err, ErrTelemetryAdapterDisconnected) && !wasUnavailable {
		c.logger.Warn("MeshCore tracking adapter unavailable", "target_public_key", target)
	}
}

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
