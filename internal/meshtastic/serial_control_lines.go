package meshtastic

import (
	"os"
	"strconv"
	"strings"
)

const serialControlLinesEnv = "LORAMAPR_MESHTASTIC_ASSERT_DTR_RTS"

func shouldAssertSerialControlLines() bool {
	raw, ok := os.LookupEnv(serialControlLinesEnv)
	if !ok {
		return false
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	enabled, err := strconv.ParseBool(value)
	if err == nil {
		return enabled
	}

	switch strings.ToLower(value) {
	case "on", "enabled", "yes", "y":
		return true
	case "off", "disabled", "no", "n":
		return false
	default:
		return false
	}
}
