package receiverevents

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/outbox"
)

type Action string

const (
	ActionRetry          Action = "retry"
	ActionQuarantine     Action = "quarantine"
	ActionPause          Action = "pause"
	ActionRequirePairing Action = "require_pairing"
	ActionStop           Action = "stop"
)

type Disposition struct {
	Action          Action
	Reason          string
	RetryAfter      time.Duration
	Quarantine      bool
	PairingRequired bool
	PauseKind       outbox.DispatchPauseKind
}

type DeliveryClient interface {
	PostNormalizedEvent(
		ctx context.Context,
		endpoint string,
		apiKey string,
		envelope []byte,
		deliveryID string,
		envelopeSHA256 string,
	) (cloudclient.NormalizedDeliveryResult, error)
}

type Dispatcher struct {
	Outbox *outbox.Engine
	Client DeliveryClient
}

type DispatchResult struct {
	DeliveryID              string
	Attempted               bool
	Acknowledged            bool
	Duplicate               bool
	Disposition             Disposition
	Quarantined             int
	Reconciled              outbox.BindingReconcileResult
	ClockAttestation        *cloudclient.ClockAttestationCandidate
	SessionEligiblePosition *cloudclient.SessionEligiblePositionAssertion
}

func (d Dispatcher) DispatchOnce(ctx context.Context, apiKey string, binding outbox.Binding, now time.Time) (DispatchResult, error) {
	if d.Outbox == nil || d.Client == nil {
		return DispatchResult{}, errors.New("normalized dispatcher requires an outbox and cloud client")
	}
	if _, err := d.Outbox.ClearResolvedPause(binding); err != nil {
		return DispatchResult{}, err
	}
	if pause, err := d.Outbox.DispatchPause(); err != nil {
		return DispatchResult{}, err
	} else if pause != nil {
		action := ActionPause
		if pause.Kind == outbox.DispatchPauseCollision {
			action = ActionStop
		}
		return DispatchResult{DeliveryID: pause.DeliveryID, Disposition: Disposition{
			Action: action, Reason: pause.Reason, PauseKind: pause.Kind,
			PairingRequired: pause.Kind != outbox.DispatchPauseCollision,
		}}, nil
	}
	reconciled, err := d.Outbox.ReconcileBinding(binding)
	if err != nil {
		return DispatchResult{}, err
	}
	delivery, err := d.Outbox.NextDue(now)
	if err != nil || delivery == nil {
		return DispatchResult{Reconciled: reconciled}, err
	}
	result := DispatchResult{DeliveryID: delivery.DeliveryID, Attempted: true, Reconciled: reconciled}
	if err := d.Outbox.MarkInflight(delivery.DeliveryID); err != nil {
		return result, err
	}
	ack, sendErr := d.Client.PostNormalizedEvent(
		ctx,
		delivery.Endpoint,
		apiKey,
		delivery.Envelope,
		delivery.DeliveryID,
		delivery.EnvelopeSHA256,
	)
	if sendErr == nil {
		if err := d.Outbox.Delete(delivery.DeliveryID); err != nil {
			return result, fmt.Errorf("remove acknowledged normalized delivery: %w", err)
		}
		result.Acknowledged = true
		result.Duplicate = ack.Duplicate
		result.ClockAttestation = ack.ClockAttestation
		result.SessionEligiblePosition = ack.SessionEligiblePosition
		return result, nil
	}

	disposition := Classify(sendErr)
	result.Disposition = disposition
	failure := failureFromError(sendErr)
	if disposition.Action == ActionPause || disposition.Action == ActionRequirePairing || disposition.Action == ActionStop {
		pause := outbox.DispatchPause{
			Kind: disposition.PauseKind, Reason: disposition.Reason, DeliveryID: delivery.DeliveryID,
			PausedAt: now.UTC(), CredentialGeneration: binding.CredentialGeneration,
			BindingGeneration: binding.BindingGeneration, StatusCode: failure.StatusCode,
			ErrorCode: failure.ErrorCode, RequestID: failure.RequestID,
		}
		return result, d.Outbox.PauseDelivery(delivery.DeliveryID, pause, failure)
	}
	if disposition.Quarantine {
		if err := d.Outbox.Quarantine(delivery.DeliveryID, disposition.Reason, failure); err != nil {
			return result, err
		}
		result.Quarantined = 1
		return result, nil
	}

	delay := disposition.RetryAfter
	if delay <= 0 {
		delay = retryDelay(delivery.Attempts)
	}
	if err := d.Outbox.Retry(delivery.DeliveryID, now.UTC().Add(delay), failure); err != nil {
		return result, err
	}
	return result, nil
}

func Classify(err error) Disposition {
	if err == nil {
		return Disposition{}
	}
	var apiErr *cloudclient.APIError
	if !errors.As(err, &apiErr) {
		return Disposition{Action: ActionRetry, Reason: "delivery_error"}
	}
	code := strings.ToUpper(strings.TrimSpace(apiErr.Code))
	base := Disposition{Reason: normalizedReason(code, apiErr.StatusCode), RetryAfter: apiErr.RetryAfter}
	switch {
	case apiErr.Retryable || apiErr.StatusCode == http.StatusTooManyRequests:
		base.Action = ActionRetry
		return base
	case apiErr.StatusCode == http.StatusUnauthorized:
		base.Action = ActionRequirePairing
		base.PairingRequired = true
		base.PauseKind = outbox.DispatchPauseCredential
		return base
	case apiErr.StatusCode == http.StatusConflict && code == "DELIVERY_ID_COLLISION":
		base.Action = ActionStop
		base.PauseKind = outbox.DispatchPauseCollision
		return base
	case apiErr.StatusCode == http.StatusForbidden && isBindingCode(code):
		base.Action = ActionPause
		base.PairingRequired = true
		base.PauseKind = outbox.DispatchPauseBinding
		return base
	case apiErr.StatusCode == http.StatusForbidden:
		base.Action = ActionPause
		base.PairingRequired = true
		base.PauseKind = outbox.DispatchPauseCredential
		return base
	case apiErr.StatusCode >= 400 && apiErr.StatusCode < 500:
		base.Action = ActionQuarantine
		base.Quarantine = true
		return base
	default:
		base.Action = ActionRetry
		return base
	}
}

func isBindingCode(code string) bool {
	return code == "RECEIVER_BINDING_MISMATCH" || code == "RECEIVER_BINDING_REQUIRED" || code == "RECEIVER_INSTALLATION_UNAVAILABLE" ||
		code == "RECEIVER_REVOKED" || code == "RECEIVER_REPLACED" || code == "RECEIVER_DISABLED"
}

func normalizedReason(code string, status int) string {
	if code != "" {
		return strings.ToLower(code)
	}
	return fmt.Sprintf("http_%d", status)
}

func failureFromError(err error) outbox.AttemptFailure {
	failure := outbox.AttemptFailure{Message: err.Error()}
	var apiErr *cloudclient.APIError
	if errors.As(err, &apiErr) {
		failure.StatusCode = apiErr.StatusCode
		failure.ErrorCode = apiErr.Code
		failure.RequestID = apiErr.RequestID
	}
	return failure
}

func retryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 5 {
		attempts = 5
	}
	delay := time.Duration(2<<attempts) * time.Second
	if delay > 2*time.Minute {
		return 2 * time.Minute
	}
	return delay
}
