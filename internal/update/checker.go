package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/manifest"
)

type StatusCode string

const (
	StatusUnknown         StatusCode = "unknown"
	StatusDisabled        StatusCode = "disabled"
	StatusCurrent         StatusCode = "current"
	StatusOutdated        StatusCode = "outdated"
	StatusChannelMismatch StatusCode = "channel_mismatch"
	StatusUnsupported     StatusCode = "unsupported"
	StatusAhead           StatusCode = "ahead"
)

type Config struct {
	Enabled             bool
	ManifestURL         string
	CheckInterval       time.Duration
	RequestTimeout      time.Duration
	MinSupportedVersion string
}

type Installed struct {
	Version     string
	Channel     string
	Platform    string
	Arch        string
	InstallType string
}

type Result struct {
	Status             StatusCode
	Summary            string
	Hint               string
	ManifestVersion    string
	ManifestChannel    string
	RecommendedVersion string
	CheckedAt          time.Time
	LastError          string
}

type Checker struct {
	cfg    Config
	now    func() time.Time
	client *http.Client
}

func NewChecker(cfg Config) *Checker {
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 6 * time.Hour
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 4 * time.Second
	}
	return &Checker{
		cfg: cfg,
		now: time.Now,
		client: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}
}

func (c *Checker) ShouldCheck(lastCheckedAt *time.Time) bool {
	if !c.cfg.Enabled || strings.TrimSpace(c.cfg.ManifestURL) == "" {
		return lastCheckedAt == nil || lastCheckedAt.IsZero()
	}
	if lastCheckedAt == nil || lastCheckedAt.IsZero() {
		return true
	}
	return !c.now().UTC().Before(lastCheckedAt.UTC().Add(c.cfg.CheckInterval))
}

func (c *Checker) Check(ctx context.Context, installed Installed) Result {
	now := c.now().UTC()
	if !c.cfg.Enabled {
		return Result{
			Status:    StatusDisabled,
			Summary:   "Update checks are disabled in config",
			Hint:      "Enable update checks and configure update.manifest_url to evaluate release freshness.",
			CheckedAt: now,
		}
	}
	manifestURL := strings.TrimSpace(c.cfg.ManifestURL)
	if manifestURL == "" {
		return Result{
			Status:    StatusUnknown,
			Summary:   "Update manifest URL is not configured",
			Hint:      "Set update.manifest_url to enable update-status reasoning.",
			CheckedAt: now,
		}
	}

	fragment, err := c.fetchManifest(ctx, manifestURL)
	if err != nil {
		return Result{
			Status:    StatusUnknown,
			Summary:   "Update manifest could not be loaded",
			Hint:      "Receiver stays online; retry later or verify manifest URL and network connectivity.",
			CheckedAt: now,
			LastError: sanitizeErr(err),
		}
	}

	out := evaluate(installed, fragment, c.cfg.MinSupportedVersion)
	out.CheckedAt = now
	return out
}

func (c *Checker) fetchManifest(ctx context.Context, manifestURL string) (manifest.Fragment, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return manifest.Fragment{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return manifest.Fragment{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return manifest.Fragment{}, fmt.Errorf("manifest fetch failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return manifest.Fragment{}, err
	}
	fragment, err := manifest.ParseFragment(payload)
	if err != nil {
		return manifest.Fragment{}, err
	}

	// Ensure payload is JSON to catch malformed responses earlier.
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return manifest.Fragment{}, err
	}

	return fragment, nil
}

func evaluate(installed Installed, fragment manifest.Fragment, minSupported string) Result {
	installedVersion := strings.TrimSpace(installed.Version)
	installedChannel := normalizeChannel(installed.Channel)
	manifestChannel := normalizeChannel(fragment.Channel)

	result := Result{
		ManifestVersion:    strings.TrimSpace(fragment.ReceiverVersion),
		ManifestChannel:    manifestChannel,
		RecommendedVersion: strings.TrimSpace(fragment.ReceiverVersion),
	}

	if installedVersion == "" {
		result.Status = StatusUnknown
		result.Summary = "Installed receiver version is not available"
		result.Hint = "Rebuild or reinstall receiver with version metadata."
		return result
	}

	if min := strings.TrimSpace(minSupported); min != "" {
		cmp, err := CompareVersions(installedVersion, min)
		if err == nil && cmp < 0 {
			result.Status = StatusUnsupported
			result.Summary = "Installed receiver version is below minimum supported version"
			result.Hint = "Upgrade receiver to a supported release before continuing normal operations."
			return result
		}
	}

	if _, ok := manifest.SelectArtifact(fragment, normalizePlatform(installed.Platform), normalizeArch(installed.Arch), ""); !ok {
		result.Status = StatusUnsupported
		result.Summary = "No compatible artifact for this platform/arch in update manifest"
		result.Hint = "Check receiver channel selection and artifact publication for this install type."
		return result
	}

	versionCmp, err := CompareVersions(installedVersion, fragment.ReceiverVersion)
	if err != nil {
		result.Status = StatusUnknown
		result.Summary = "Installed or recommended version could not be compared"
		result.Hint = "Ensure semantic version format is used in build and manifest metadata."
		return result
	}

	if installedChannel != "" && manifestChannel != "" && installedChannel != manifestChannel {
		result.Status = StatusChannelMismatch
		result.Summary = "Installed receiver channel does not match manifest channel"
		result.Hint = fmt.Sprintf("Installed channel %q differs from manifest channel %q.", installedChannel, manifestChannel)
		return result
	}

	switch {
	case versionCmp < 0:
		result.Status = StatusOutdated
		result.Summary = "Receiver is behind recommended release"
		result.Hint = "Upgrade receiver using the supported package or appliance image path."
	case versionCmp > 0:
		result.Status = StatusAhead
		result.Summary = "Receiver is newer than manifest recommendation"
		result.Hint = "This is expected for beta/dev testing; confirm intended channel."
	default:
		result.Status = StatusCurrent
		result.Summary = "Receiver is on the recommended release"
		result.Hint = "No update action required."
	}
	return result
}

func normalizeChannel(channel string) string {
	value := strings.ToLower(strings.TrimSpace(channel))
	switch value {
	case "", "stable":
		return "stable"
	case "beta":
		return "beta"
	case "dev":
		return "dev"
	default:
		return value
	}
}

func normalizePlatform(platform string) string {
	value := strings.ToLower(strings.TrimSpace(platform))
	switch value {
	case "darwin":
		return "macos"
	case "raspberrypi", "pi":
		return "raspberry_pi"
	default:
		return value
	}
}

func normalizeArch(arch string) string {
	value := strings.ToLower(strings.TrimSpace(arch))
	switch value {
	case "arm", "armhf":
		return "armv7"
	default:
		return value
	}
}

func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if len(text) > 256 {
		return text[:256]
	}
	return text
}
