package buildinfo

import "strings"

var (
	Version   = "dev"
	Channel   = "dev"
	Commit    = ""
	BuildDate = ""
	BuildID   = ""
)

type Info struct {
	Version   string `json:"version"`
	Channel   string `json:"channel"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
	BuildID   string `json:"build_id,omitempty"`
}

func Current() Info {
	out := Info{
		Version: strings.TrimSpace(Version),
		Channel: strings.TrimSpace(Channel),
		Commit:  strings.TrimSpace(Commit),
	}
	if out.Version == "" {
		out.Version = "dev"
	}
	if out.Channel == "" {
		out.Channel = "dev"
	}
	if value := strings.TrimSpace(BuildDate); value != "" {
		out.BuildDate = value
	}
	if value := strings.TrimSpace(BuildID); value != "" {
		out.BuildID = value
	} else if out.Commit != "" {
		out.BuildID = out.Commit
	}
	return out
}
