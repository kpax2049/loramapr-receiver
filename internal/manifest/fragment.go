package manifest

import (
	"encoding/json"
	"errors"
	"strings"
)

type Artifact struct {
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Kind        string `json:"kind"`
	Format      string `json:"format"`
	Filename    string `json:"filename"`
	RelativeURL string `json:"relativeUrl"`
	SHA256      string `json:"checksumSha256"`
	SizeBytes   int64  `json:"sizeBytes"`
	Recommended bool   `json:"recommended"`
}

type Fragment struct {
	SchemaVersion   string     `json:"schemaVersion"`
	ReceiverVersion string     `json:"receiverVersion"`
	Channel         string     `json:"channel"`
	Artifacts       []Artifact `json:"artifacts"`
}

func ParseFragment(payload []byte) (Fragment, error) {
	var fragment Fragment
	if err := json.Unmarshal(payload, &fragment); err != nil {
		return Fragment{}, err
	}
	if strings.TrimSpace(fragment.ReceiverVersion) == "" {
		return Fragment{}, errors.New("manifest receiverVersion is required")
	}
	if strings.TrimSpace(fragment.Channel) == "" {
		fragment.Channel = "stable"
	}
	return fragment, nil
}

func SelectArtifact(fragment Fragment, platform string, arch string, kind string) (Artifact, bool) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	arch = strings.ToLower(strings.TrimSpace(arch))
	kind = strings.ToLower(strings.TrimSpace(kind))

	matches := make([]Artifact, 0)
	for _, artifact := range fragment.Artifacts {
		if strings.ToLower(strings.TrimSpace(artifact.Platform)) != platform {
			continue
		}
		if strings.ToLower(strings.TrimSpace(artifact.Arch)) != arch {
			continue
		}
		if kind != "" && strings.ToLower(strings.TrimSpace(artifact.Kind)) != kind {
			continue
		}
		matches = append(matches, artifact)
	}
	if len(matches) == 0 {
		return Artifact{}, false
	}
	best := matches[0]
	for _, candidate := range matches[1:] {
		if candidate.Recommended {
			best = candidate
			break
		}
	}
	return best, true
}
