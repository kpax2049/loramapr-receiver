# Debian-family Package Lifecycle Behavior

This document defines install/upgrade/remove/reinstall behavior for the
`loramapr-receiver` `.deb` path.

## Policy Summary

- Package name: `loramapr-receiver`
- Service unit path: `/lib/systemd/system/loramapr-receiverd.service`
- Config file: `/etc/loramapr/receiver.json` (`conffile`, preserved across upgrade/remove)
- State file: `/var/lib/loramapr/receiver-state.json` (preserved on remove)
- State/log directories:
  - `/var/lib/loramapr`
  - `/var/log/loramapr`

## Fresh Install

On `apt install loramapr-receiver`:

1. Package payload installs binary + service unit + default config.
2. `postinst` ensures service user/group `loramapr` exists.
3. Runtime directories are created/chowned.
4. Service is enabled and started.

Expected result: receiver reaches pairing-ready service state.

## Upgrade (Package -> Package)

On `apt upgrade`/`apt install` newer version:

1. Existing config and state are preserved.
2. `prerm upgrade` stops the running service.
3. New package payload replaces binary/unit.
4. `postinst` reloads systemd and restarts service if it is enabled.

If service was explicitly disabled before upgrade, upgrade does not force-enable
it.

## Migration from Tarball/Systemd Layout (Practical Path)

If host was previously installed via systemd tarball layout:

- Package installation reuses existing `/etc/loramapr/receiver.json` and state.
- `postinst` normalizes legacy unit handling:
  - if `/etc/systemd/system/loramapr-receiverd.service` is identical to packaged
    `/lib/systemd/system/loramapr-receiverd.service`, the legacy copy is removed.
  - if it differs, it is kept as an override and a migration warning is emitted.

This avoids destructive behavior for user-customized service units.

## Remove

On `apt remove loramapr-receiver`:

- service is stopped and disabled
- package payload is removed
- config/state remain on disk

This supports reinstall without losing pairing/runtime state.

## Purge

On `apt purge loramapr-receiver`:

- same remove behavior plus:
  - `/etc/loramapr/receiver.json` removed
  - `/var/lib/loramapr/receiver-state.json` removed
  - `/var/lib/loramapr` and `/var/log/loramapr` removed

Use purge only when intentionally resetting local receiver identity/state.

## Reinstall After Remove

After `apt remove`, reinstalling with `apt install`:

- reuses preserved config/state when present
- reenables and starts service by default

## Validation and Smoke Checks

Maintainer validation scripts:

- structure: `packaging/debian/validate-deb.sh <deb-file>`
- lifecycle policy: `packaging/debian/validate-lifecycle.sh <deb-file>`

See `docs/reviewer-smoke-test.md` for end-to-end install and lifecycle checks.
