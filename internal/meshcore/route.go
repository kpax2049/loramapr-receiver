package meshcore

import (
	"encoding/hex"
	"fmt"
)

// RouteMode describes the route selected for a request.  "direct" is
// deliberately not a mode: MeshCore uses it for both zero-hop and an explicit
// cached route.
type RouteMode string

const (
	RouteModeZeroHop      RouteMode = "zero_hop"
	RouteModeExplicitPath RouteMode = "explicit_path"
	RouteModeFlood        RouteMode = "flood"
	RouteModeUnknown      RouteMode = "unknown"

	contactOutPathUnknown = byte(0xff)
)

// RouteEvidence is request-side Companion evidence. Path entries are route
// hashes, not repeater identities.
type RouteEvidence struct {
	Mode       RouteMode `json:"mode"`
	Path       []string  `json:"path,omitempty"`
	PathLength int       `json:"pathLength"`
	Source     string    `json:"source"`
}

func unknownRouteEvidence(source string) RouteEvidence {
	return RouteEvidence{Mode: RouteModeUnknown, Source: source}
}

// routeEvidenceFromContactOutPath decodes the same compact path encoding
// MeshCore stores in ContactInfo.out_path. It must not be presented as node
// identity: each item is only a route hash.
func routeEvidenceFromContactOutPath(pathEncoding byte, pathBytes []byte) RouteEvidence {
	if pathEncoding == contactOutPathUnknown {
		return RouteEvidence{Mode: RouteModeFlood, Source: "contact_out_path"}
	}
	hashCount := int(pathEncoding & 0x3f)
	hashSize := int(pathEncoding>>6) + 1
	if hashSize == 4 || hashCount*hashSize > len(pathBytes) {
		return unknownRouteEvidence("contact_out_path_invalid")
	}
	if hashCount == 0 {
		return RouteEvidence{Mode: RouteModeZeroHop, Source: "contact_out_path"}
	}
	path := make([]string, 0, hashCount)
	for offset := 0; offset < hashCount*hashSize; offset += hashSize {
		path = append(path, hex.EncodeToString(pathBytes[offset:offset+hashSize]))
	}
	return RouteEvidence{Mode: RouteModeExplicitPath, Path: path, PathLength: hashCount, Source: "contact_out_path"}
}

func (r RouteEvidence) copy() RouteEvidence {
	r.Path = append([]string(nil), r.Path...)
	return r
}

func (r RouteEvidence) logValue() string {
	return fmt.Sprintf("%s/%d/%s", r.Mode, r.PathLength, r.Source)
}
