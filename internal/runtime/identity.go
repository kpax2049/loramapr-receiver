package runtime

import (
	"os"
	"strings"
	"unicode"
)

const maxIdentityHintLength = 80

func runtimeHostName() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return sanitizeLocalNameHint(host)
}

func resolveLocalNameHint(configValue, persistedValue, hostname, installType, installationID string) string {
	if value := sanitizeLocalNameHint(configValue); value != "" {
		return value
	}
	if value := sanitizeLocalNameHint(persistedValue); value != "" {
		return value
	}
	return defaultLocalNameHint(hostname, installType, installationID)
}

func defaultLocalNameHint(hostname, installType, installationID string) string {
	host := sanitizeToken(hostname)
	suffix := shortInstallID(installationID)
	if host == "" {
		host = installPrefix(installType)
	}
	if suffix == "" {
		return trimLength(host)
	}
	return trimLength(host + "-" + suffix)
}

func installPrefix(installType string) string {
	switch strings.ToLower(strings.TrimSpace(installType)) {
	case "pi-appliance":
		return "pi-receiver"
	case "linux-package":
		return "linux-receiver"
	case "windows-user":
		return "windows-receiver"
	default:
		return "receiver"
	}
}

func sanitizeLocalNameHint(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	prevSpace := false
	for _, r := range trimmed {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			prevSpace = false
		case r == '-', r == '_', r == '.', r == ':', r == '/':
			b.WriteRune(r)
			prevSpace = false
		case unicode.IsSpace(r):
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		default:
			// drop unsupported punctuation/control chars
		}
	}
	return trimLength(strings.TrimSpace(b.String()))
}

func sanitizeToken(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-_")
	return trimLength(out)
}

func shortInstallID(installationID string) string {
	value := strings.TrimSpace(installationID)
	if len(value) >= 6 {
		return strings.ToLower(value[len(value)-6:])
	}
	return strings.ToLower(value)
}

func trimLength(value string) string {
	if len(value) <= maxIdentityHintLength {
		return value
	}
	return strings.TrimSpace(value[:maxIdentityHintLength])
}
