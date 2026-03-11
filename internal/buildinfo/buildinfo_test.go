package buildinfo

import "testing"

func TestCurrentAppliesDefaults(t *testing.T) {
	t.Parallel()

	originalVersion := Version
	originalChannel := Channel
	originalCommit := Commit
	originalBuildDate := BuildDate
	defer func() {
		Version = originalVersion
		Channel = originalChannel
		Commit = originalCommit
		BuildDate = originalBuildDate
	}()

	Version = ""
	Channel = ""
	Commit = " abc123 "
	BuildDate = " 2026-03-11T00:00:00Z "

	info := Current()
	if info.Version != "dev" {
		t.Fatalf("expected default version dev, got %q", info.Version)
	}
	if info.Channel != "dev" {
		t.Fatalf("expected default channel dev, got %q", info.Channel)
	}
	if info.Commit != "abc123" {
		t.Fatalf("unexpected commit value %q", info.Commit)
	}
	if info.BuildDate != "2026-03-11T00:00:00Z" {
		t.Fatalf("unexpected build date %q", info.BuildDate)
	}
}
