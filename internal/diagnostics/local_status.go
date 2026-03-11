package diagnostics

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/status"
)

type LocalStatusProbe struct {
	Status   string           `json:"status"`
	Detail   string           `json:"detail,omitempty"`
	URL      string           `json:"url,omitempty"`
	Snapshot *status.Snapshot `json:"snapshot,omitempty"`
}

func ProbeLocalRuntimeStatus(bindAddress string, timeout time.Duration) LocalStatusProbe {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(bindAddress))
	if err != nil || strings.TrimSpace(port) == "" {
		return LocalStatusProbe{
			Status: "invalid",
			Detail: "portal bind address is invalid",
		}
	}

	host = normalizePortalHost(host)
	target := net.JoinHostPort(host, port)
	url := "http://" + target + "/api/status"

	req, reqErr := http.NewRequest(http.MethodGet, url, nil)
	if reqErr != nil {
		return LocalStatusProbe{
			Status: "invalid",
			Detail: reqErr.Error(),
			URL:    url,
		}
	}
	client := &http.Client{Timeout: timeout}
	resp, doErr := client.Do(req)
	if doErr != nil {
		return LocalStatusProbe{
			Status: "unreachable",
			Detail: doErr.Error(),
			URL:    url,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LocalStatusProbe{
			Status: "unreachable",
			Detail: "status API returned non-success",
			URL:    url,
		}
	}

	var snap status.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return LocalStatusProbe{
			Status: "unreachable",
			Detail: "status API payload could not be parsed",
			URL:    url,
		}
	}

	return LocalStatusProbe{
		Status:   "reachable",
		URL:      url,
		Snapshot: &snap,
	}
}

func normalizePortalHost(host string) string {
	value := strings.Trim(strings.TrimSpace(host), "[]")
	switch value {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return value
	}
}
