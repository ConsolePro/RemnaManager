package network

import "net"

func Addresses() (string, string, error) {
	v4, v6 := "Not connected", "Not connected"
	interfaces, err := net.Interfaces()
	if err != nil {
		return v4, v6, err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addresses {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || !ip.IsGlobalUnicast() {
				continue
			}
			if ip.To4() != nil && v4 == "Not connected" {
				v4 = ip.String()
			}
			if ip.To4() == nil && v6 == "Not connected" {
				v6 = ip.String()
			}
		}
	}
	return v4, v6, nil
}
