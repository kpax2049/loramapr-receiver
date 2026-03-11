package release

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildManifestIncludesLinuxAndPiEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "loramapr-receiver_v1.1.0_linux_arm64_systemd.tar.gz"), []byte("a"))
	mustWrite(t, filepath.Join(dir, "loramapr-receiver_v1.1.0_linux_amd64.tar.gz"), []byte("b"))
	mustWrite(t, filepath.Join(dir, "loramapr-receiver_v1.1.0_windows_amd64.zip"), []byte("c"))
	mustWrite(t, filepath.Join(dir, "SHA256SUMS"), []byte(
		"aaaabbbb loramapr-receiver_v1.1.0_linux_arm64_systemd.tar.gz\n"+
			"ccccdddd loramapr-receiver_v1.1.0_linux_amd64.tar.gz\n"+
			"eeeeffff loramapr-receiver_v1.1.0_windows_amd64.zip\n",
	))

	manifest, err := BuildManifest(BuildOptions{
		Version:      "v1.1.0",
		Channel:      "stable",
		ArtifactsDir: dir,
	})
	if err != nil {
		t.Fatalf("BuildManifest returned error: %v", err)
	}

	if manifest.SchemaVersion == "" {
		t.Fatal("expected schema version")
	}
	if manifest.ReceiverVersion != "v1.1.0" {
		t.Fatalf("unexpected receiver version: %s", manifest.ReceiverVersion)
	}

	var hasLinux bool
	var hasPi bool
	for _, artifact := range manifest.Artifacts {
		if artifact.Platform == "linux" && artifact.Arch == "arm64" && artifact.Kind == "systemd_layout" {
			hasLinux = true
		}
		if artifact.Platform == "raspberry_pi" && artifact.Arch == "arm64" && artifact.Kind == "systemd_layout" {
			hasPi = true
			if !artifact.Recommended {
				t.Fatal("expected raspberry_pi arm64 systemd artifact to be recommended")
			}
		}
	}

	if !hasLinux {
		t.Fatal("expected linux arm64 systemd artifact entry")
	}
	if !hasPi {
		t.Fatal("expected raspberry_pi arm64 systemd artifact entry")
	}
}

func TestBuildManifestRequiresChecksums(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "loramapr-receiver_v1.1.0_linux_amd64.tar.gz"), []byte("x"))
	mustWrite(t, filepath.Join(dir, "SHA256SUMS"), []byte(""))

	_, err := BuildManifest(BuildOptions{
		Version:      "v1.1.0",
		Channel:      "stable",
		ArtifactsDir: dir,
	})
	if err == nil {
		t.Fatal("expected checksum validation error")
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
