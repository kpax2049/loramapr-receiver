# macOS Notarization Placeholder

Future expansion will add:

- package signing identities
- notarization submission and stapling automation
- release artifact promotion for `.pkg`/`.dmg` outputs

Current release pipeline emits cross-compiled Darwin binaries only.

Hook placeholder:

- `signing-hook.sh`
  - documents expected CI/environment inputs for future Developer ID signing and
    notarization integration.
