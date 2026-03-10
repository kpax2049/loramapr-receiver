package state

import (
	"path/filepath"
	"testing"
)

func TestOpenInitializesDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	snapshot := store.Snapshot()
	if snapshot.Installation.ID == "" {
		t.Fatal("expected installation ID to be generated")
	}
	if snapshot.Pairing.Phase != PairingUnpaired {
		t.Fatalf("expected default pairing phase %q, got %q", PairingUnpaired, snapshot.Pairing.Phase)
	}
	if snapshot.Installation.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be populated")
	}
}

func TestUpdatePersistsAcrossRestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	err = store.Update(func(data *Data) {
		data.Pairing.Phase = PairingActivated
		data.Cloud.EndpointURL = "https://api.example.com"
		data.Cloud.ReceiverID = "rx-123"
		data.Runtime.Profile = "linux-service"
		data.Runtime.Mode = "service"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	store2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}

	snapshot := store2.Snapshot()
	if snapshot.Pairing.Phase != PairingActivated {
		t.Fatalf("expected phase %q, got %q", PairingActivated, snapshot.Pairing.Phase)
	}
	if snapshot.Cloud.EndpointURL != "https://api.example.com" {
		t.Fatalf("unexpected endpoint: %q", snapshot.Cloud.EndpointURL)
	}
	if snapshot.Cloud.ReceiverID != "rx-123" {
		t.Fatalf("unexpected receiver id: %q", snapshot.Cloud.ReceiverID)
	}
	if snapshot.Runtime.Profile != "linux-service" {
		t.Fatalf("unexpected profile: %q", snapshot.Runtime.Profile)
	}
	if snapshot.Runtime.Mode != "service" {
		t.Fatalf("unexpected mode: %q", snapshot.Runtime.Mode)
	}
}
