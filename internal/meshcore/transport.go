package meshcore

import "context"

// TransportMetadata describes only the local carrier used to exchange complete
// Companion frames. It is deliberately separate from MeshCore identities and
// evidence/trust semantics, which remain owned by the session and normalizer.
type TransportMetadata struct {
	Kind                   string
	DelegatedAdvertAllowed bool
	PeerSelector           string
}

// CompanionTransport opens one local carrier to a MeshCore Companion.
// Implementations must present complete opcode+payload Companion frames to the
// shared session, never transport-framed bytes.
type CompanionTransport interface {
	Open(context.Context) (CompanionLink, error)
}

// CompanionLink exchanges complete MeshCore Companion frames. The 176-byte
// payload limit is enforced by each transport implementation before frames
// reach CompanionSession.
type CompanionLink interface {
	ReadFrame(context.Context) ([]byte, error)
	WriteFrame(context.Context, []byte) error
	Metadata() TransportMetadata
	Close() error
}
