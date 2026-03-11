package update

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/manifest"
)

func TestCompareVersions(t *testing.T) {
	t.Parallel()

	if cmp, err := CompareVersions("v2.4.0", "v2.3.0"); err != nil || cmp <= 0 {
		t.Fatalf("expected v2.4.0 > v2.3.0, cmp=%d err=%v", cmp, err)
	}
	if cmp, err := CompareVersions("v2.4.0-beta.1", "v2.4.0"); err != nil || cmp >= 0 {
		t.Fatalf("expected prerelease to be lower than release, cmp=%d err=%v", cmp, err)
	}
}

func TestEvaluateStatuses(t *testing.T) {
	t.Parallel()

	fragment := mustParseFragment(t, `{
	  "schemaVersion":"receiver-manifest-fragment/v1",
	  "receiverVersion":"v2.4.0",
	  "channel":"stable",
	  "artifacts":[
	    {"platform":"linux","arch":"amd64","kind":"deb_package","format":"deb","filename":"a.deb","relativeUrl":"receiver/stable/v2.4.0/a.deb","checksumSha256":"abc","sizeBytes":1,"recommended":true}
	  ]
	}`)

	cases := []struct {
		name string
		in   Installed
		min  string
		want StatusCode
	}{
		{
			name: "current",
			in: Installed{
				Version:  "v2.4.0",
				Channel:  "stable",
				Platform: "linux",
				Arch:     "amd64",
			},
			want: StatusCurrent,
		},
		{
			name: "outdated",
			in: Installed{
				Version:  "v2.3.0",
				Channel:  "stable",
				Platform: "linux",
				Arch:     "amd64",
			},
			want: StatusOutdated,
		},
		{
			name: "channel mismatch",
			in: Installed{
				Version:  "v2.4.0",
				Channel:  "beta",
				Platform: "linux",
				Arch:     "amd64",
			},
			want: StatusChannelMismatch,
		},
		{
			name: "unsupported version",
			in: Installed{
				Version:  "v2.1.0",
				Channel:  "stable",
				Platform: "linux",
				Arch:     "amd64",
			},
			min:  "v2.2.0",
			want: StatusUnsupported,
		},
		{
			name: "unsupported platform",
			in: Installed{
				Version:  "v2.4.0",
				Channel:  "stable",
				Platform: "linux",
				Arch:     "arm64",
			},
			want: StatusUnsupported,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := evaluate(tc.in, fragment, tc.min)
			if result.Status != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, result.Status)
			}
		})
	}
}

func TestCheckerCheckFetchFailure(t *testing.T) {
	t.Parallel()

	checker := NewChecker(Config{
		Enabled:        true,
		ManifestURL:    "http://127.0.0.1:1/unreachable",
		CheckInterval:  10 * time.Minute,
		RequestTimeout: 20 * time.Millisecond,
	})
	result := checker.Check(context.Background(), Installed{
		Version:  "v2.4.0",
		Channel:  "stable",
		Platform: "linux",
		Arch:     "amd64",
	})
	if result.Status != StatusUnknown {
		t.Fatalf("expected unknown status on fetch failure, got %q", result.Status)
	}
	if result.LastError == "" {
		t.Fatalf("expected last error to be populated")
	}
}

func TestCheckerShouldCheck(t *testing.T) {
	t.Parallel()

	checker := NewChecker(Config{
		Enabled:        true,
		ManifestURL:    "https://downloads.example/manifest.json",
		CheckInterval:  2 * time.Hour,
		RequestTimeout: 2 * time.Second,
	})
	now := time.Date(2026, 3, 11, 15, 0, 0, 0, time.UTC)
	checker.now = func() time.Time { return now }

	if !checker.ShouldCheck(nil) {
		t.Fatalf("expected nil last check to trigger check")
	}
	last := now.Add(-3 * time.Hour)
	if !checker.ShouldCheck(&last) {
		t.Fatalf("expected stale last check to trigger check")
	}
	last = now.Add(-30 * time.Minute)
	if checker.ShouldCheck(&last) {
		t.Fatalf("expected recent last check to skip check")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCheckerHTTPPath(t *testing.T) {
	t.Parallel()

	checker := NewChecker(Config{
		Enabled:        true,
		ManifestURL:    "https://downloads.example/receiver/stable/latest/cloud-manifest.fragment.json",
		CheckInterval:  6 * time.Hour,
		RequestTimeout: 2 * time.Second,
	})
	checker.client = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host != "downloads.example" {
				t.Fatalf("unexpected manifest host: %s", req.URL.Host)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(`{
		  "schemaVersion":"receiver-manifest-fragment/v1",
		  "receiverVersion":"v2.4.0",
		  "channel":"stable",
		  "artifacts":[
		    {"platform":"linux","arch":"amd64","kind":"deb_package","format":"deb","filename":"a.deb","relativeUrl":"receiver/stable/v2.4.0/a.deb","checksumSha256":"abc","sizeBytes":1,"recommended":true}
		  ]
		}`)),
			}, nil
		}),
	}

	result := checker.Check(context.Background(), Installed{
		Version:  "v2.4.0",
		Channel:  "stable",
		Platform: "linux",
		Arch:     "amd64",
	})
	if result.Status != StatusCurrent {
		t.Fatalf("expected current status, got %q", result.Status)
	}
}

func mustParseFragment(t *testing.T, payload string) manifest.Fragment {
	t.Helper()
	out, err := manifest.ParseFragment([]byte(payload))
	if err != nil {
		t.Fatalf("ParseFragment returned error: %v", err)
	}
	return out
}
