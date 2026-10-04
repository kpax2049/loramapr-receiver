package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/outbox"
	"github.com/loramapr/loramapr-receiver/internal/state"
)

type installationRotator struct {
	state  *state.Store
	outbox *outbox.Engine
	now    func() time.Time
	newID  func() (string, error)
}

func newInstallationRotator(stateStore *state.Store, outboxEngine *outbox.Engine) *installationRotator {
	return &installationRotator{state: stateStore, outbox: outboxEngine, now: time.Now, newID: newInstallationID}
}

func newInstallationID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate installation identity: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func (r *installationRotator) RotateInstallation(reason string) (string, error) {
	if r == nil || r.state == nil || r.outbox == nil {
		return "", errors.New("installation rotation coordinator is unavailable")
	}
	journal, err := r.outbox.InstallationRotation()
	if err != nil {
		return "", err
	}
	if journal == nil || journal.Phase == outbox.InstallationRotationCompleted {
		oldID := strings.TrimSpace(r.state.Snapshot().Installation.ID)
		if oldID == "" {
			return "", errors.New("current installation identity is missing")
		}
		newID, err := r.newID()
		if err != nil {
			return "", err
		}
		journal, err = r.outbox.BeginInstallationRotation(outbox.InstallationRotation{
			OldInstallationID: oldID,
			NewInstallationID: newID,
			Reason:            strings.TrimSpace(reason),
		})
		if err != nil {
			return "", err
		}
	}
	return r.recover(journal)
}

func (r *installationRotator) Recover() (string, error) {
	if r == nil || r.state == nil || r.outbox == nil {
		return "", errors.New("installation rotation coordinator is unavailable")
	}
	journal, err := r.outbox.InstallationRotation()
	if err != nil || journal == nil {
		return "", err
	}
	if journal.Phase == outbox.InstallationRotationCompleted {
		current := r.state.Snapshot()
		if current.Installation.ID != journal.NewInstallationID {
			return "", errors.New("completed installation rotation does not match durable receiver state")
		}
		return journal.NewInstallationID, nil
	}
	return r.recover(journal)
}

func (r *installationRotator) recover(journal *outbox.InstallationRotation) (string, error) {
	if journal == nil {
		return "", errors.New("installation rotation journal is missing")
	}
	var err error
	if journal.Phase == outbox.InstallationRotationIntent {
		journal, err = r.outbox.QuarantineInstallationRotation()
		if err != nil {
			return "", fmt.Errorf("quarantine old installation evidence: %w", err)
		}
	}
	if journal.Phase == outbox.InstallationRotationOldBindingQuarantined {
		current := r.state.Snapshot()
		switch current.Installation.ID {
		case journal.OldInstallationID:
			if err := r.state.Update(func(data *state.Data) {
				data.Installation.ID = journal.NewInstallationID
				data.Installation.Bound = false
				data.Installation.CreatedAt = journal.StartedAt.UTC()
				data.Pairing.Phase = state.PairingUnpaired
				data.Pairing.PairingCode = ""
				data.Pairing.InstallSessionID = ""
				data.Pairing.FlowKey = ""
				data.Pairing.ActivationToken = ""
				data.Pairing.ActivationExpires = nil
				data.Pairing.RetryCount = 0
				data.Pairing.NextRetryAt = nil
				data.Cloud.OwnerID = ""
				data.Cloud.ReceiverID = ""
				data.Cloud.ReceiverLabel = ""
				data.Cloud.SiteLabel = ""
				data.Cloud.GroupLabel = ""
				data.Cloud.IngestAPIKeyID = ""
				data.Cloud.IngestAPIKey = ""
				data.Cloud.CredentialRef = ""
				data.Cloud.ClockSamples = nil
				data.Cloud.UpdatedAt = r.now().UTC()
			}); err != nil {
				return "", fmt.Errorf("persist rotated installation identity: %w", err)
			}
		case journal.NewInstallationID:
			if err := validateRotatedState(current); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("receiver state installation %q matches neither rotation boundary", current.Installation.ID)
		}
		journal, err = r.outbox.AdvanceInstallationRotation(
			outbox.InstallationRotationOldBindingQuarantined,
			outbox.InstallationRotationStateRotated,
		)
		if err != nil {
			return "", fmt.Errorf("record rotated installation state: %w", err)
		}
	}
	if journal.Phase == outbox.InstallationRotationStateRotated {
		journal, err = r.outbox.AdvanceInstallationRotation(
			outbox.InstallationRotationStateRotated,
			outbox.InstallationRotationCompleted,
		)
		if err != nil {
			return "", fmt.Errorf("complete installation rotation: %w", err)
		}
	}
	if journal.Phase != outbox.InstallationRotationCompleted {
		return "", fmt.Errorf("installation rotation stopped in unexpected phase %q", journal.Phase)
	}
	return journal.NewInstallationID, nil
}

func validateRotatedState(current state.Data) error {
	if current.Installation.Bound || strings.TrimSpace(current.Cloud.OwnerID) != "" ||
		strings.TrimSpace(current.Cloud.ReceiverID) != "" || strings.TrimSpace(current.Cloud.IngestAPIKey) != "" {
		return errors.New("rotated installation state still contains the old cloud binding")
	}
	if len(current.Cloud.ClockSamples) != 0 {
		return errors.New("rotated installation state still contains cloud clock samples")
	}
	return nil
}
