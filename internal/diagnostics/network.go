package diagnostics

import (
	"net"
	"strings"
)

type NetworkProbe struct {
	Status    string `json:"status"`
	Interface string `json:"interface,omitempty"`
	Address   string `json:"address,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

func ProbeLocalNetwork() NetworkProbe {
	interfaces, err := net.Interfaces()
	if err != nil {
		return NetworkProbe{
			Status: "unavailable",
			Detail: "failed to read network interfaces: " + err.Error(),
		}
	}

	foundUp := false
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		foundUp = true
		addrs, addrErr := iface.Addrs()
		if addrErr != nil {
			continue
		}
		for _, addr := range addrs {
			ip := extractIP(addr)
			if ip == nil {
				continue
			}
			if ip.IsLoopback() || ip.IsLinkLocalMulticast() {
				continue
			}
			if !ip.IsGlobalUnicast() {
				continue
			}
			return NetworkProbe{
				Status:    "available",
				Interface: iface.Name,
				Address:   ip.String(),
			}
		}
	}

	if foundUp {
		return NetworkProbe{
			Status: "unavailable",
			Detail: "no active network address assigned",
		}
	}

	return NetworkProbe{
		Status: "unavailable",
		Detail: "no active network interface detected",
	}
}

func NetworkAvailable(probe NetworkProbe) (bool, bool) {
	status := strings.ToLower(strings.TrimSpace(probe.Status))
	switch status {
	case "available":
		return true, true
	case "unavailable":
		return false, true
	default:
		return false, false
	}
}

func NetworkComponentState(probe NetworkProbe) (string, string) {
	switch strings.ToLower(strings.TrimSpace(probe.Status)) {
	case "available":
		message := "network available"
		if probe.Interface != "" {
			message += " via " + probe.Interface
		}
		if probe.Address != "" {
			message += " (" + probe.Address + ")"
		}
		return "available", message
	case "unavailable":
		message := "network unavailable"
		if strings.TrimSpace(probe.Detail) != "" {
			message += ": " + strings.TrimSpace(probe.Detail)
		}
		return "unavailable", message
	default:
		message := "network status unknown"
		if strings.TrimSpace(probe.Detail) != "" {
			message += ": " + strings.TrimSpace(probe.Detail)
		}
		return "unknown", message
	}
}

func extractIP(addr net.Addr) net.IP {
	switch value := addr.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		return nil
	}
}
