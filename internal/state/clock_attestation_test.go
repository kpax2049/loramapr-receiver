package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/clockattestation"
)

func TestClockSamplesPreservedAcrossKeyRotationAndClearedAcrossBindingChange(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	sample := clockattestation.Sample{Token: "v1.a.b", Nonce: "nonce", ReceiverAgentID: "agent-a",
		InstallationID: store.Snapshot().Installation.ID, ReceivedAt: time.Now().UTC(), IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.Update(func(data *Data) {
		data.Cloud.OwnerID = "owner-a"
		data.Cloud.ReceiverID = "agent-a"
		data.Cloud.IngestAPIKey = "key-a"
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(data *Data) { data.Cloud.ClockSamples = []clockattestation.Sample{sample} }); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(data *Data) { data.Cloud.IngestAPIKey = "key-b" }); err != nil {
		t.Fatal(err)
	}
	if got := len(store.Snapshot().Cloud.ClockSamples); got != 1 {
		t.Fatalf("key rotation samples = %d, want 1", got)
	}
	if err := store.Update(func(data *Data) { data.Cloud.ReceiverID = "agent-b" }); err != nil {
		t.Fatal(err)
	}
	if got := len(store.Snapshot().Cloud.ClockSamples); got != 0 {
		t.Fatalf("binding change samples = %d, want 0", got)
	}
}

func TestSchemaNineMigratesToTen(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().SchemaVersion; got != 10 {
		t.Fatalf("schema = %d, want 10", got)
	}
}
