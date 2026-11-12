package routing

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func exitOutbound(exit services.RoutingExit, pools []services.RoutingPool) (map[string]any, error) {
	var outbound map[string]any
	switch exit.Kind {
	case "vless":
		var err error
		outbound, err = vlessOutbound(exit.URL)
		if err != nil {
			return nil, err
		}
	case "wireguard":
		wg := exit.WireGuard
		if wg == nil {
			return nil, invalid("Импортируйте WireGuard-параметры для WARP-выхода.", "Import WireGuard parameters for the WARP outbound.")
		}
		for _, key := range []string{wg.SecretKey, wg.PeerPublicKey} {
			bytes, err := base64.StdEncoding.DecodeString(key)
			if err != nil || len(bytes) != 32 {
				return nil, invalid("Ключи WireGuard должны содержать 32 байта в Base64.", "WireGuard keys must contain 32 Base64-encoded bytes.")
			}
		}
		if len(wg.Address) == 0 || len(wg.Address) > 2 {
			return nil, invalid("Укажите IPv4/IPv6-адрес интерфейса WireGuard.", "Specify WireGuard interface IPv4/IPv6 addresses.")
		}
		for _, address := range wg.Address {
			if _, _, err := net.ParseCIDR(address); err != nil {
				return nil, invalid("Неверный адрес WireGuard.", "Invalid WireGuard interface address.")
			}
		}
		if err := endpoint(wg.Endpoint); err != nil {
			return nil, err
		}
		if len(wg.Reserved) != 0 && len(wg.Reserved) != 3 {
			return nil, invalid("Поле reserved WireGuard должно содержать три числа 0–255.", "WireGuard reserved must contain three numbers from 0 to 255.")
		}
		for _, v := range wg.Reserved {
			if v < 0 || v > 255 {
				return nil, invalid("Неверное значение reserved WireGuard.", "Invalid WireGuard reserved value.")
			}
		}
		mtu := wg.MTU
		if mtu == 0 {
			mtu = 1280
		}
		if mtu < 1280 || mtu > 1500 {
			return nil, invalid("MTU WireGuard должен быть в диапазоне 1280–1500.", "WireGuard MTU must be in the range 1280–1500.")
		}
		allowed := wg.AllowedIPs
		if len(allowed) == 0 {
			return nil, invalid("В профиле WireGuard не указаны AllowedIPs.", "WireGuard profile has no AllowedIPs.")
		}
		for _, value := range allowed {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return nil, invalid("Некорректные AllowedIPs WireGuard.", "Invalid WireGuard AllowedIPs.")
			}
		}
		settings := map[string]any{"secretKey": wg.SecretKey, "address": wg.Address, "mtu": mtu, "noKernelTun": true, "peers": []any{map[string]any{"publicKey": wg.PeerPublicKey, "endpoint": wg.Endpoint, "keepAlive": 25, "allowedIPs": allowed}}}
		if len(wg.Reserved) > 0 {
			settings["reserved"] = wg.Reserved
		}
		outbound = map[string]any{"protocol": "wireguard", "settings": settings}
	default:
		return nil, invalid("Неизвестный тип дополнительного выхода.", "Unknown explicit outbound type.")
	}
	outbound["tag"] = "rm-" + exit.Name
	if exit.ViaPool != "" {
		if exit.Kind != "vless" {
			return nil, invalid("Цепочка через подключение пока поддерживается для VLESS-выходов.", "Dialer chaining is supported for VLESS outbounds.")
		}
		found := false
		for _, pool := range pools {
			if pool.Name == exit.ViaPool && len(pool.Hosts) == 1 {
				found = true
			}
		}
		if !found {
			return nil, invalid("Для входа в мост выберите группу ровно с одним подключением.", "Bridge entry must be a group with exactly one host.")
		}
		stream, _ := outbound["streamSettings"].(map[string]any)
		if stream == nil {
			stream = map[string]any{}
		}
		stream["sockopt"] = map[string]any{"dialerProxy": "rm-" + exit.ViaPool + "."}
		outbound["streamSettings"] = stream
	}
	return outbound, nil
}

func endpoint(value string) error {
	host, port, err := net.SplitHostPort(value)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || n < 1 || n > 65535 || host == "" || strings.IndexFunc(host, unicode.IsControl) >= 0 || strings.ContainsAny(host, " /\\\t\r\n?#@") {
		return invalid("Неверный адрес выхода: нужен домен/IP и порт.", "Outbound endpoint must contain a host and port.")
	}
	return nil
}

func vlessOutbound(value string) (map[string]any, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "vless" || u.User == nil || !uuidPattern.MatchString(u.User.Username()) || u.Path != "" && u.Path != "/" {
		return nil, invalid("Укажите корректную VLESS-ссылку транзитного пользователя.", "Provide a valid service-user VLESS URI.")
	}
	if _, password := u.User.Password(); password {
		return nil, invalid("В VLESS-ссылке не должно быть пароля после UUID.", "VLESS URI must not include a password after the UUID.")
	}
	if err := endpoint(u.Host); err != nil {
		return nil, err
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, invalid("Некорректные параметры VLESS-ссылки.", "Invalid VLESS URI query.")
	}
	allowed := map[string]bool{"encryption": true, "flow": true, "type": true, "security": true, "sni": true, "fp": true, "pbk": true, "sid": true, "spx": true, "path": true, "host": true, "serviceName": true, "mode": true, "alpn": true}
	for key, values := range q {
		if !allowed[key] || len(values) != 1 {
			return nil, invalid("VLESS-ссылка содержит неподдерживаемый параметр. Используйте RAW/TCP, WS, gRPC или XHTTP с обычными параметрами.", "VLESS URI contains an unsupported parameter. Use standard RAW/TCP, WS, gRPC or XHTTP options.")
		}
	}
	flow := q.Get("flow")
	if flow != "" && flow != "xtls-rprx-vision" {
		return nil, invalid("Неподдерживаемый flow VLESS.", "Unsupported VLESS flow.")
	}
	encryption := q.Get("encryption")
	if encryption == "" {
		encryption = "none"
	}
	if encryption != "none" {
		return nil, invalid("Для транзитной ссылки пока поддерживается encryption=none.", "Service URI currently supports encryption=none.")
	}
	user := map[string]any{"id": u.User.Username(), "encryption": encryption}
	if flow != "" {
		user["flow"] = flow
	}
	port, _ := strconv.Atoi(u.Port())
	out := map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": u.Hostname(), "port": port, "users": []any{user}}}}}
	network := q.Get("type")
	if network == "" || network == "tcp" {
		network = "raw"
	}
	stream := map[string]any{"network": network}
	switch network {
	case "raw":
	case "ws":
		stream["wsSettings"] = map[string]any{"path": q.Get("path"), "headers": map[string]string{"Host": q.Get("host")}}
	case "grpc":
		mode := q.Get("mode")
		if mode != "" && mode != "gun" && mode != "multi" {
			return nil, invalid("Неподдерживаемый режим gRPC.", "Unsupported gRPC mode.")
		}
		stream["grpcSettings"] = map[string]any{"serviceName": q.Get("serviceName"), "multiMode": mode == "multi"}
	case "xhttp":
		mode := q.Get("mode")
		if mode == "" {
			mode = "auto"
		}
		if mode != "auto" && mode != "packet-up" && mode != "stream-up" && mode != "stream-one" {
			return nil, invalid("Неподдерживаемый режим XHTTP.", "Unsupported XHTTP mode.")
		}
		stream["xhttpSettings"] = map[string]any{"path": q.Get("path"), "host": q.Get("host"), "mode": mode}
	default:
		return nil, invalid("Неподдерживаемый транспорт VLESS.", "Unsupported VLESS transport.")
	}
	security := q.Get("security")
	if security == "" {
		security = "none"
	}
	stream["security"] = security
	if flow != "" && (network != "raw" || security == "none") {
		return nil, invalid("Vision требует RAW/TCP с TLS или Reality.", "Vision requires RAW/TCP with TLS or Reality.")
	}
	switch security {
	case "none":
	case "tls":
		settings := map[string]any{"serverName": q.Get("sni")}
		if q.Get("fp") != "" {
			settings["fingerprint"] = q.Get("fp")
		}
		if q.Get("alpn") != "" {
			settings["alpn"] = strings.Split(q.Get("alpn"), ",")
		}
		stream["tlsSettings"] = settings
	case "reality":
		key, err := base64.RawURLEncoding.DecodeString(q.Get("pbk"))
		if err != nil || len(key) != 32 || q.Get("sni") == "" {
			return nil, invalid("Для Reality нужны корректные pbk и sni.", "Reality requires a valid pbk and sni.")
		}
		sid := q.Get("sid")
		if len(sid) > 16 || len(sid)%2 != 0 || strings.IndexFunc(sid, func(r rune) bool { return !strings.ContainsRune("0123456789abcdefABCDEF", r) }) >= 0 {
			return nil, invalid("Неверный shortId Reality.", "Invalid Reality shortId.")
		}
		fp := q.Get("fp")
		if fp == "" {
			fp = "chrome"
		}
		stream["realitySettings"] = map[string]any{"serverName": q.Get("sni"), "fingerprint": fp, "publicKey": q.Get("pbk"), "shortId": sid, "spiderX": q.Get("spx")}
	default:
		return nil, invalid("Неподдерживаемый security VLESS.", "Unsupported VLESS security.")
	}
	out["streamSettings"] = stream
	return out, nil
}

func explicitTarget(spec services.RoutingSpec, target string) map[string]any {
	for _, exit := range spec.Exits {
		if exit.Name == target {
			return map[string]any{"outboundTag": "rm-" + target}
		}
	}
	return targetRule(target)
}

// ParseWireGuard reads a wg-quick profile without executing configuration.
func ParseWireGuard(data []byte) (*services.RoutingWireGuard, error) {
	if len(data) > 64<<10 {
		return nil, invalid("Файл WireGuard слишком большой.", "WireGuard profile is too large.")
	}
	wg := &services.RoutingWireGuard{MTU: 1280}
	section := ""
	peers := 0
	seen := map[string]bool{}
	interfaces := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line)
			if section == "[interface]" {
				interfaces++
			}
			if section != "[interface]" && section != "[peer]" {
				return nil, invalid("Неизвестная секция WireGuard.", "Unknown WireGuard section.")
			}
			if section == "[peer]" {
				peers++
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, invalid("Некорректная строка WireGuard.", "Invalid WireGuard profile line.")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "privatekey", "publickey", "endpoint", "mtu":
			if seen[section+key] {
				return nil, invalid("Повторяющийся параметр WireGuard.", "Duplicate WireGuard parameter.")
			}
			seen[section+key] = true
		}
		switch {
		case section == "[interface]" && key == "privatekey":
			wg.SecretKey = value
		case section == "[interface]" && key == "address":
			for _, part := range strings.Split(value, ",") {
				wg.Address = append(wg.Address, strings.TrimSpace(part))
			}
		case section == "[interface]" && key == "mtu":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, invalid("Некорректный MTU WireGuard.", "Invalid WireGuard MTU.")
			}
			wg.MTU = n
		case section == "[peer]" && key == "publickey":
			wg.PeerPublicKey = value
		case section == "[peer]" && key == "endpoint":
			wg.Endpoint = value
		case section == "[peer]" && key == "allowedips":
			for _, part := range strings.Split(value, ",") {
				wg.AllowedIPs = append(wg.AllowedIPs, strings.TrimSpace(part))
			}
		case key == "reserved":
			if wg.Reserved != nil {
				return nil, invalid("Повторяющийся reserved WireGuard.", "Duplicate WireGuard reserved.")
			}
			for _, part := range strings.Split(strings.Trim(value, "[]"), ",") {
				n, err := strconv.Atoi(strings.TrimSpace(part))
				if err != nil {
					return nil, invalid("Некорректный reserved WireGuard.", "Invalid WireGuard reserved.")
				}
				wg.Reserved = append(wg.Reserved, n)
			}
		case key == "presharedkey":
			return nil, invalid("Профили WireGuard с PreSharedKey пока не поддерживаются клиентским Xray.", "WireGuard profiles with PreSharedKey are not supported by this client recipe.")
		}
	}
	if peers != 1 || interfaces != 1 {
		return nil, invalid("В профиле WireGuard должен быть ровно один Peer.", "WireGuard profile must contain exactly one peer.")
	}
	if _, err := exitOutbound(services.RoutingExit{Name: "warp", Kind: "wireguard", WireGuard: wg}, nil); err != nil {
		return nil, err
	}
	return wg, nil
}
