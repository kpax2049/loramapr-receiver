package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/loramapr/loramapr-receiver/internal/release"
)

func main() {
	var (
		version      = flag.String("version", "", "release version, for example v1.1.0")
		channel      = flag.String("channel", "stable", "release channel, for example stable or beta")
		artifactsDir = flag.String("artifacts-dir", "", "artifact directory containing SHA256SUMS")
		manifestOut  = flag.String("manifest-out", "", "output path for manifest fragment json")
		metadataOut  = flag.String("metadata-out", "", "optional output path for release metadata json")
		urlPrefix    = flag.String("url-prefix", "receiver", "relative URL prefix used in manifest entries")
		gitCommit    = flag.String("git-commit", "", "git commit to include in metadata")
	)
	flag.Parse()

	if strings.TrimSpace(*version) == "" {
		fatal("version is required")
	}
	if strings.TrimSpace(*artifactsDir) == "" {
		fatal("artifacts-dir is required")
	}
	if strings.TrimSpace(*manifestOut) == "" {
		fatal("manifest-out is required")
	}

	manifest, err := release.BuildManifest(release.BuildOptions{
		Version:      *version,
		Channel:      *channel,
		ArtifactsDir: *artifactsDir,
		URLPrefix:    *urlPrefix,
	})
	if err != nil {
		fatal(err.Error())
	}
	if err := release.WriteManifest(*manifestOut, manifest); err != nil {
		fatal(err.Error())
	}

	metadataPath := strings.TrimSpace(*metadataOut)
	if metadataPath != "" {
		commit := strings.TrimSpace(*gitCommit)
		if commit == "" {
			commit = resolveCommit()
		}
		metadata := release.BuildReleaseMetadata(*version, *channel, commit, manifest)
		if err := release.WriteJSON(metadataPath, metadata); err != nil {
			fatal(err.Error())
		}
	}
}

func resolveCommit() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
