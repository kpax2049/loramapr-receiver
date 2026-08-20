package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/outbox"
	"github.com/loramapr/loramapr-receiver/internal/state"
)

func TestInstallationRotationQuarantinesOldEvidenceAndRotatesBinding(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	stateStore, oldID := boundRotationState(t)
	engine, outboxStore := rotationOutbox(t, now)
	defer closeRotationOutbox(t, engine, outboxStore)

	oldPending := rotationDelivery("0198c7a2-e395-7000-8000-000000000060", oldID)
	oldPaused := rotationDelivery("0198c7a2-e395-7000-8000-000000000061", oldID)
	other := rotationDelivery("0198c7a2-e395-7000-8000-000000000062", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	alreadyQuarantined := rotationDelivery("0198c7a2-e395-7000-8000-000000000063", oldID)
	for _, delivery := range []outbox.Delivery{oldPending, oldPaused, other, alreadyQuarantined} {
		if err := outboxStore.Enqueue(delivery); err != nil {
			t.Fatal(err)
		}
	}
	if err := outboxStore.MarkInflight(oldPaused.DeliveryID); err != nil {
		t.Fatal(err)
	}
	if err := outboxStore.PauseDelivery(oldPaused.DeliveryID, outbox.DispatchPause{
		Kind: outbox.DispatchPauseCredential, Reason: "http_401",
	}, outbox.AttemptFailure{StatusCode: 401}); err != nil {
		t.Fatal(err)
	}
	if err := outboxStore.Quarantine(alreadyQuarantined.DeliveryID, "preexisting", outbox.AttemptFailure{}); err != nil {
		t.Fatal(err)
	}

	rotator := newInstallationRotator(stateStore, engine)
	rotator.now = func() time.Time { return now }
	rotator.newID = func() (string, error) { return "fedcba9876543210fedcba9876543210", nil }
	newID, err := rotator.RotateInstallation("re_pair")
	if err != nil {
		t.Fatal(err)
	}
	if newID != "fedcba9876543210fedcba9876543210" {
		t.Fatalf("new installation ID=%q", newID)
	}
	snapshot := stateStore.Snapshot()
	if snapshot.Installation.ID != newID || snapshot.Installation.Bound || snapshot.Cloud.OwnerID != "" || snapshot.Cloud.ReceiverID != "" || snapshot.Cloud.IngestAPIKey != "" {
		t.Fatalf("rotated state retained old binding: %#v", snapshot)
	}
	journal, err := engine.InstallationRotation()
	if err != nil || journal == nil || journal.Phase != outbox.InstallationRotationCompleted || journal.Quarantined != 2 {
		t.Fatalf("rotation journal=%#v err=%v", journal, err)
	}
	for _, id := range []string{oldPending.DeliveryID, oldPaused.DeliveryID} {
		record, err := engine.Get(id)
		if err != nil || record.State != outbox.StateQuarantined || record.QuarantineReason != "installation_rotated" {
			t.Fatalf("old delivery %s=%#v err=%v", id, record, err)
		}
	}
	record, err := engine.Get(alreadyQuarantined.DeliveryID)
	if err != nil || record.QuarantineReason != "preexisting" {
		t.Fatalf("preexisting quarantine changed: %#v err=%v", record, err)
	}
	record, err = engine.Get(other.DeliveryID)
	if err != nil || record.State != outbox.StatePending {
		t.Fatalf("different installation delivery changed: %#v err=%v", record, err)
	}
	pause, err := engine.DispatchPause()
	if err != nil || pause != nil {
		t.Fatalf("old installation pause survived: %#v err=%v", pause, err)
	}
	if err := stateStore.Update(func(data *state.Data) {
		data.Installation.Bound = true
		data.Cloud.OwnerID = "owner-2"
		data.Cloud.ReceiverID = "agent-2"
		data.Cloud.IngestAPIKey = "secret-2"
	}); err != nil {
		t.Fatal(err)
	}
	if recovered, err := rotator.Recover(); err != nil || recovered != newID {
		t.Fatalf("completed journal blocked activated installation restart: recovered=%q err=%v", recovered, err)
	}
}

func TestInstallationRotationRecoversEveryDurablePhaseAfterRestart(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name         string
		phase        outbox.InstallationRotationPhase
		stateWritten bool
	}{
		{name: "intent", phase: outbox.InstallationRotationIntent},
		{name: "old_binding_quarantined", phase: outbox.InstallationRotationOldBindingQuarantined},
		{name: "state_written_before_journal", phase: outbox.InstallationRotationOldBindingQuarantined, stateWritten: true},
		{name: "state_rotated", phase: outbox.InstallationRotationStateRotated, stateWritten: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateStore, oldID := boundRotationState(t)
			path := filepath.Join(t.TempDir(), "outbox.db")
			engine, outboxStore := rotationOutboxAt(t, path, now)
			newID := "00112233445566778899aabbccddeeff"
			if _, err := engine.BeginInstallationRotation(outbox.InstallationRotation{
				OldInstallationID: oldID, NewInstallationID: newID, Reason: "crash_test",
			}); err != nil {
				t.Fatal(err)
			}
			if test.phase != outbox.InstallationRotationIntent {
				if _, err := engine.QuarantineInstallationRotation(); err != nil {
					t.Fatal(err)
				}
			}
			if test.stateWritten {
				persistRotatedState(t, stateStore, newID, now)
			}
			if test.phase == outbox.InstallationRotationStateRotated {
				if _, err := engine.AdvanceInstallationRotation(outbox.InstallationRotationOldBindingQuarantined, outbox.InstallationRotationStateRotated); err != nil {
					t.Fatal(err)
				}
			}
			closeRotationOutbox(t, engine, outboxStore)

			engine, outboxStore = rotationOutboxAt(t, path, now)
			defer closeRotationOutbox(t, engine, outboxStore)
			rotator := newInstallationRotator(stateStore, engine)
			rotator.now = func() time.Time { return now }
			got, err := rotator.Recover()
			if err != nil || got != newID {
				t.Fatalf("recover=%q err=%v", got, err)
			}
			journal, err := engine.InstallationRotation()
			if err != nil || journal.Phase != outbox.InstallationRotationCompleted || stateStore.Snapshot().Installation.ID != newID {
				t.Fatalf("recovery journal=%#v state=%#v err=%v", journal, stateStore.Snapshot().Installation, err)
			}
		})
	}
}

func boundRotationState(t *testing.T) (*state.Store, string) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	oldID := store.Snapshot().Installation.ID
	if err := store.Update(func(data *state.Data) {
		data.Installation.Bound = true
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.OwnerID = "owner-1"
		data.Cloud.ReceiverID = "agent-1"
		data.Cloud.IngestAPIKey = "secret-1"
	}); err != nil {
		t.Fatal(err)
	}
	return store, oldID
}

func persistRotatedState(t *testing.T, store *state.Store, newID string, now time.Time) {
	t.Helper()
	if err := store.Update(func(data *state.Data) {
		data.Installation.ID = newID
		data.Installation.Bound = false
		data.Installation.CreatedAt = now
		data.Cloud.OwnerID = ""
		data.Cloud.ReceiverID = ""
		data.Cloud.IngestAPIKey = ""
	}); err != nil {
		t.Fatal(err)
	}
}

func rotationOutbox(t *testing.T, now time.Time) (*outbox.Engine, *outbox.Store) {
	t.Helper()
	return rotationOutboxAt(t, filepath.Join(t.TempDir(), "outbox.db"), now)
}

func rotationOutboxAt(t *testing.T, path string, now time.Time) (*outbox.Engine, *outbox.Store) {
	t.Helper()
	store, err := outbox.Open(outbox.Config{Path: path, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := outbox.NewEngine(store, outbox.EngineConfig{})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return engine, store
}

func closeRotationOutbox(t *testing.T, engine *outbox.Engine, store *outbox.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func rotationDelivery(id, installationID string) outbox.Delivery {
	return outbox.Delivery{
		DeliveryID: id, Envelope: []byte(`{}`), EnvelopeSHA256: strings.Repeat("a", 64), IdempotencyKey: id,
		OwnerID: "owner-1", ReceiverAgentIDSnapshot: "agent-1", InstallationID: installationID,
		Endpoint: "/api/receiver/events/v1",
	}
}
