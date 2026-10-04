# MeshCore Companion v1.17.1 source-derived fixtures

These fixtures are deterministic, source-derived examples. They are **not**
captures from physical hardware.

Pinned upstream:

- release: Companion v1.17.1, 2026-08-14;
- source commit: `d92964352441e53b93e8667b802e04f6e072b39e`;
- framing: `src/helpers/BaseSerialInterface.h` and
  `src/helpers/ArduinoSerialInterface.cpp`;
- commands, replies and push opcodes: `examples/companion_radio/MyMesh.cpp`;
- release constants: `examples/companion_radio/MyMesh.h`.

`device-info-v1.17.1.hex` follows the exact 82-byte `RESP_CODE_DEVICE_INFO`
layout written by `MyMesh.cpp`. Its model and numeric settings are deterministic
test values; build `14 Aug 2026`, version `v1.17.1`, and protocol code 13 are the
pinned allowlist values. Decoded-byte SHA-256:
`ea432a9f78717d42348bb0127c5b4b4bed69d3250f1451ae1269b94141ea3beb`.

`self-info-v1.17.1.hex` follows the variable-length `RESP_CODE_SELF_INFO`
layout with the deterministic public key bytes `00..1f`, fixed radio values,
and node name `Fixture Node`. Decoded-byte SHA-256:
`e39872688d1cde81c892f31717985d44d197f76f8a2c6d4a843dcb817aa554d9`.

`new-advert-v1.17.1.hex` follows the exact 148-byte
`PUSH_CODE_NEW_ADVERT` contact response layout. Its key, contact fields,
position and timestamps are deterministic test values. The Companion push is
evidence that pinned firmware already accepted an advert; it does not contain
the original on-air signature and is not itself device attestation.

`signed-log-rx-advert-v1.hex` is a source-derived `PUSH_CODE_LOG_RX_DATA`
control payload containing a complete version-1 direct-route ADVERT packet.
It is generated with Go/Node-compatible RFC 8032 Ed25519 using the fixed
32-byte seed `000102...1f`, timestamp `2026-08-20T12:00:00Z`, direct path
hashes `aa,bb`, signed E6 coordinates, and UTF-8 name `Fixture Node`. The
signature covers the exact upstream `Mesh.cpp` bytes: public key, four
little-endian timestamp bytes, and application data. This deterministic key is
test-only and must never be used as an operational identity.
Decoded-byte SHA-256:
`5f761d58e5785b361f771fc259c3c94f1ec23b5926ae0d4d8e115c44b343b6af`.

The generation recipe is the field order and little-endian integer encoding in
the pinned `MyMesh.cpp` response writers, with fixed strings NUL-padded to their
declared widths. The `.hex` files contain only the Companion protocol payload;
physical `<`/`>` marker and little-endian length bytes are added by framing
tests. Recreate them only from the pinned layouts and re-record these decoded
byte hashes when an intentional fixture change is reviewed.

Physical Companion captures remain required by the later hardware E2E gate.
