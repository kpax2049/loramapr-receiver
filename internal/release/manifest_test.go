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
	mustWrite(t, filepath.Join(dir, "loramapr-receiver_v1.1.0_linux_arm64.deb"), []byte("d"))
	mustWrite(t, filepath.Join(dir, "loramapr-receiver_v1.1.0_pi_arm64.img.xz"), []byte("img"))
	mustWrite(t, filepath.Join(dir, "loramapr-receiver_v1.1.0_windows_amd64.zip"), []byte("c"))
	mustWrite(t, filepath.Join(dir, "SHA256SUMS"), []byte(
		"aaaabbbb loramapr-receiver_v1.1.0_linux_arm64_systemd.tar.gz\n"+
			"ccccdddd loramapr-receiver_v1.1.0_linux_amd64.tar.gz\n"+
			"11112222 loramapr-receiver_v1.1.0_linux_arm64.deb\n"+
			"33334444 loramapr-receiver_v1.1.0_pi_arm64.img.xz\n"+
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
	var hasLinuxDeb bool
	var hasPiDeb bool
	var hasPiImage bool
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
		if artifact.Platform == "linux" && artifact.Arch == "arm64" && artifact.Kind == "deb_package" {
			hasLinuxDeb = true
			if !artifact.Recommended {
				t.Fatal("expected linux arm64 deb artifact to be recommended")
			}
		}
		if artifact.Platform == "raspberry_pi" && artifact.Arch == "arm64" && artifact.Kind == "deb_package" {
			hasPiDeb = true
			if !artifact.Recommended {
				t.Fatal("expected raspberry_pi arm64 deb artifact to be recommended")
			}
		}
		if artifact.Platform == "raspberry_pi" && artifact.Arch == "arm64" && artifact.Kind == "appliance_image" {
			hasPiImage = true
			if !artifact.Recommended {
				t.Fatal("expected raspberry_pi appliance image artifact to be recommended")
			}
		}
	}

	if !hasLinux {
		t.Fatal("expected linux arm64 systemd artifact entry")
	}
	if !hasPi {
		t.Fatal("expected raspberry_pi arm64 systemd artifact entry")
	}
	if !hasLinuxDeb {
		t.Fatal("expected linux arm64 deb artifact entry")
	}
	if !hasPiDeb {
		t.Fatal("expected raspberry_pi arm64 deb artifact entry")
	}
	if !hasPiImage {
		t.Fatal("expected raspberry_pi arm64 appliance image artifact entry")
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

func TestParseArtifactNameDebArmhf(t *testing.T) {
	t.Parallel()

	kind, format, goos, arch, err := parseArtifactName("v2.1.0", "loramapr-receiver_v2.1.0_linux_armhf.deb")
	if err != nil {
		t.Fatalf("parseArtifactName returned error: %v", err)
	}
	if kind != "deb_package" {
		t.Fatalf("unexpected kind: %s", kind)
	}
	if format != "deb" {
		t.Fatalf("unexpected format: %s", format)
	}
	if goos != "linux" {
		t.Fatalf("unexpected goos: %s", goos)
	}
	if arch != "armv7" {
		t.Fatalf("expected armv7 arch mapping, got: %s", arch)
	}
}

func TestParseArtifactNamePiImage(t *testing.T) {
	t.Parallel()

	kind, format, goos, arch, err := parseArtifactName("v2.2.0", "loramapr-receiver_v2.2.0_pi_arm64.img.xz")
	if err != nil {
		t.Fatalf("parseArtifactName returned error: %v", err)
	}
	if kind != "appliance_image" {
		t.Fatalf("unexpected kind: %s", kind)
	}
	if format != "img.xz" {
		t.Fatalf("unexpected format: %s", format)
	}
	if goos != "pi" {
		t.Fatalf("unexpected goos: %s", goos)
	}
	if arch != "arm64" {
		t.Fatalf("unexpected arch: %s", arch)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
