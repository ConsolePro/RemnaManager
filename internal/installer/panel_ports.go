package installer

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func requiredPanelPorts(p services.PanelParameters, layout int) map[int]bool {
	ports := map[int]bool{443: true, 3000: true}
	if p.Proxy == "caddy" || layout < officialLayout {
		ports[80] = true
	}
	if p.RemoteSubscription == nil {
		ports[3010] = true
	}
	if layout >= officialLayout {
		ports[3001] = true
		ports[6767] = true
		if p.Proxy == "nginx" {
			ports[8443] = true
		}
	}
	if protectionMode(p) == "tinyauth" {
		ports[3020] = true
	}
	if protectionMode(p) == "mfa" {
		ports[3030] = true
	}
	return ports
}

func (r *Real) checkPanelPorts(ctx context.Context, p services.PanelParameters, layout int, managed bool) error {
	data, err := r.Runner.Run(ctx, nil, "ss", "-H", "-ltnp")
	if err != nil {
		return err
	}
	required := requiredPanelPorts(p, layout)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		_, port, _ := net.SplitHostPort(fields[3])
		number, _ := strconv.Atoi(port)
		if required[number] && !managed {
			return fmt.Errorf("port %d is already in use; no existing service will be stopped", number)
		}
	}
	return nil
}
