// Package routing composes Remnawave XRAY_JSON subscription templates. Dynamic
// host credentials are injected by the panel. Explicit service credentials are
// private manager state but will be delivered in recipient subscriptions.
// Hosts, node profiles and squad membership are never edited here.
package routing

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

var uuidPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{1,47}$`)
var poolPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}$`)
var geoPattern = regexp.MustCompile(`^geo(ip|site):!?[A-Za-z0-9_@-]+$`)

func ValidGroupName(name string) bool {
	return poolPattern.MatchString(name) && name != "direct" && name != "block"
}

func invalid(ru, en string) error { return services.LocalizedError(ru, en) }

func Validate(spec services.RoutingSpec) error {
	if !namePattern.MatchString(spec.Name) || strings.TrimSpace(spec.Name) != spec.Name {
		return invalid("Имя схемы: 2–48 латинских букв, цифр, пробелов, дефисов или подчёркиваний.", "Scheme name must be 2–48 Latin letters, digits, spaces, dashes or underscores.")
	}
	if !uuidPattern.MatchString(spec.BaseTemplateUUID) {
		return invalid("Выберите исходный шаблон XRAY_JSON.", "Select a base XRAY_JSON template.")
	}
	if len(spec.Pools)+len(spec.Exits) == 0 || len(spec.Pools) > 16 || len(spec.Exits) > 16 || len(spec.Rules) > 128 {
		return invalid("Нужна хотя бы одна группа выходов; максимум 16 групп и 128 правил.", "Use 1–16 outbound groups and no more than 128 rules.")
	}
	targets := map[string]bool{"direct": true, "block": true}
	for _, pool := range spec.Pools {
		if !poolPattern.MatchString(pool.Name) || targets[pool.Name] || len(pool.Hosts) == 0 || len(pool.Hosts) > 64 {
			return invalid("Укажите уникальные имена групп (латиница и цифры) и 1–64 подключения в каждой.", "Use unique alphanumeric group names with 1–64 connections each.")
		}
		targets[pool.Name] = true
		seen := map[string]bool{}
		for _, id := range pool.Hosts {
			if !uuidPattern.MatchString(id) || seen[id] {
				return invalid("Группа содержит неверный или повторяющийся UUID подключения.", "Group contains an invalid or duplicate host UUID.")
			}
			seen[id] = true
		}
	}
	for _, exit := range spec.Exits {
		if !poolPattern.MatchString(exit.Name) || targets[exit.Name] {
			return invalid("Выходы и группы должны иметь уникальные короткие имена.", "Outbounds and groups must have unique short names.")
		}
		if len(exit.URL) > 8192 {
			return invalid("Транзитная ссылка слишком длинная.", "Service URI is too long.")
		}
		if _, err := exitOutbound(exit, spec.Pools); err != nil {
			return err
		}
		targets[exit.Name] = true
	}
	if !targets[spec.DefaultTarget] {
		return invalid("Выберите существующий выход по умолчанию.", "Choose an existing default outbound group.")
	}
	for _, rule := range spec.Rules {
		if !targets[rule.Target] {
			return invalid("Правило ссылается на неизвестную группу выходов.", "Rule refers to an unknown outbound group.")
		}
		if len(rule.Domains)+len(rule.IPs)+len(rule.Protocols) == 0 && rule.Network == "" && rule.Port == "" {
			return invalid("У правила должно быть условие. Общий маршрут задаётся выходом по умолчанию.", "A rule needs a match condition. Use the default target for catch-all routing.")
		}
		if len(rule.Domains)+len(rule.IPs) > 512 {
			return invalid("Слишком много условий в одном правиле (максимум 512).", "A rule may contain at most 512 domain/IP conditions.")
		}
		for _, domain := range rule.Domains {
			if err := validateDomain(domain); err != nil {
				return err
			}
		}
		for _, ip := range rule.IPs {
			if strings.HasPrefix(ip, "geoip:") && geoPattern.MatchString(ip) {
				continue
			}
			if net.ParseIP(ip) != nil {
				continue
			}
			if _, _, err := net.ParseCIDR(ip); err != nil {
				return invalid("Неверный IP/CIDR или geoip-категория.", "Invalid IP/CIDR or geoip category.")
			}
		}
		if rule.Network != "" && rule.Network != "tcp" && rule.Network != "udp" && rule.Network != "tcp,udp" {
			return invalid("Сеть правила: tcp, udp или tcp,udp.", "Rule network must be tcp, udp or tcp,udp.")
		}
		for _, p := range rule.Protocols {
			if p != "http" && p != "tls" && p != "quic" && p != "bittorrent" {
				return invalid("Неизвестный протокол правила.", "Unknown routing protocol matcher.")
			}
		}
		if rule.Port != "" {
			for _, part := range strings.Split(rule.Port, ",") {
				bounds := strings.Split(part, "-")
				if len(bounds) > 2 {
					return invalid("Неверный порт или диапазон портов.", "Invalid port or port range.")
				}
				prev := 0
				for _, v := range bounds {
					n, err := strconv.Atoi(v)
					if err != nil || n < 1 || n > 65535 || n < prev {
						return invalid("Порты должны быть в диапазоне 1–65535.", "Ports must be in the range 1–65535.")
					}
					prev = n
				}
			}
		}
	}
	return nil
}

func validateDomain(value string) error {
	bad := len(value) == 0 || len(value) > 512 || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0
	if strings.HasPrefix(value, "regexp:") {
		_, err := regexp.Compile(strings.TrimPrefix(value, "regexp:"))
		bad = bad || err != nil
	} else if strings.HasPrefix(value, "geosite:") {
		bad = bad || !geoPattern.MatchString(value)
	} else {
		domain := value
		for _, prefix := range []string{"domain:", "full:", "keyword:"} {
			domain = strings.TrimPrefix(domain, prefix)
		}
		bad = bad || domain == "" || strings.ContainsAny(domain, " /\\:@?#")
	}
	if bad {
		return invalid("Неверное доменное условие маршрутизации.", "Invalid routing domain matcher.")
	}
	return nil
}

func targetRule(target string) map[string]any {
	if target == "direct" || target == "block" {
		return map[string]any{"outboundTag": "rm-" + target}
	}
	return map[string]any{"balancerTag": "rm-" + target}
}

// Render produces a new template, preserving unrelated base fields and all
// constrained base rules. Only unconditional base defaults are replaced.
func Render(spec services.RoutingSpec, base json.RawMessage) (json.RawMessage, error) {
	if err := Validate(spec); err != nil {
		return nil, err
	}
	var result map[string]any
	if len(base) > 2<<20 || json.Unmarshal(base, &result) != nil || result == nil {
		return nil, invalid("Исходный шаблон не содержит корректный JSON-объект.", "Base template must be a valid JSON object.")
	}
	if result["remnawave"] != nil {
		return nil, invalid("Исходный шаблон уже содержит injectHosts. Выберите исходный шаблон без инжекта.", "Base template already has an injector; choose a base without injection.")
	}
	if _, ok := result["inbounds"].([]any); !ok {
		return nil, invalid("В исходном клиентском шаблоне нет массива inbounds.", "Base client template has no inbounds array.")
	}
	outbounds, ok := result["outbounds"].([]any)
	if !ok {
		return nil, invalid("В исходном шаблоне нет массива outbounds.", "Base template has no outbounds array.")
	}
	staticTags := map[string]bool{}
	for _, item := range outbounds {
		ob, ok := item.(map[string]any)
		if !ok {
			return nil, invalid("Некорректный outbound в исходном шаблоне.", "Invalid base outbound.")
		}
		tag, _ := ob["tag"].(string)
		if strings.HasPrefix(tag, "rm-") || staticTags[tag] || tag == "" {
			return nil, invalid("Теги исходных выходов должны быть уникальны и не начинаться с rm-.", "Base outbound tags must be unique and must not start with rm-.")
		}
		staticTags[tag] = true
	}
	outbounds = append(outbounds, map[string]any{"tag": "rm-direct", "protocol": "freedom"}, map[string]any{"tag": "rm-block", "protocol": "blackhole"})
	result["outbounds"] = outbounds
	for _, exit := range spec.Exits {
		out, err := exitOutbound(exit, spec.Pools)
		if err != nil {
			return nil, err
		}
		outbounds = append(outbounds, out)
	}
	result["outbounds"] = outbounds
	groups, balancers, subjects := []any{}, []any{}, []any{}
	for _, pool := range spec.Pools {
		prefix := "rm-" + pool.Name + "."
		groups = append(groups, map[string]any{"selector": map[string]any{"type": "uuids", "values": pool.Hosts}, "tagPrefix": prefix, "selectFrom": "ALL"})
		balancers = append(balancers, map[string]any{"tag": "rm-" + pool.Name, "selector": []string{prefix}, "strategy": map[string]any{"type": "leastPing"}, "fallbackTag": "rm-block"})
		subjects = append(subjects, prefix)
	}
	result["remnawave"] = map[string]any{"injectHosts": groups}
	route, ok := result["routing"].(map[string]any)
	if !ok && result["routing"] != nil {
		return nil, invalid("Некорректная секция routing исходного шаблона.", "Invalid base routing section.")
	}
	if route == nil {
		route = map[string]any{}
	}
	if existing, ok := route["balancers"].([]any); ok {
		seen := map[string]bool{}
		for _, value := range existing {
			item, ok := value.(map[string]any)
			tag, _ := item["tag"].(string)
			if !ok || tag == "" || strings.HasPrefix(tag, "rm-") || seen[tag] {
				return nil, invalid("Теги исходных балансировщиков должны быть уникальны и не начинаться с rm-.", "Base balancer tags must be unique and must not start with rm-.")
			}
			seen[tag] = true
		}
		balancers = append(existing, balancers...)
	} else if route["balancers"] != nil {
		return nil, invalid("Некорректные балансировщики исходного шаблона.", "Invalid base balancers.")
	}
	route["balancers"] = balancers
	var rules, baseRules []any
	if list, ok := route["rules"].([]any); ok {
		for _, value := range list {
			rule, ok := value.(map[string]any)
			if !ok {
				return nil, invalid("Некорректное правило в исходном шаблоне.", "Invalid base routing rule.")
			}
			constrained := false
			for key := range rule {
				if key != "type" && key != "outboundTag" && key != "balancerTag" && key != "network" {
					constrained = true
				}
			}
			if !constrained {
				continue
			}
			if rule["outboundTag"] == "proxy" && !staticTags["proxy"] {
				delete(rule, "outboundTag")
				for k, v := range explicitTarget(spec, spec.DefaultTarget) {
					rule[k] = v
				}
			}
			baseRules = append(baseRules, rule)
		}
	} else if route["rules"] != nil {
		return nil, invalid("Правила исходного шаблона должны быть массивом.", "Base routing rules must be an array.")
	}
	for _, rule := range spec.Rules {
		add := func(domains, ips []string) {
			item := explicitTarget(spec, rule.Target)
			item["type"] = "field"
			if len(domains) > 0 {
				item["domain"] = domains
			}
			if len(ips) > 0 {
				item["ip"] = ips
			}
			if len(rule.Protocols) > 0 {
				item["protocol"] = rule.Protocols
			}
			if rule.Network != "" {
				item["network"] = rule.Network
			}
			if rule.Port != "" {
				item["port"] = rule.Port
			}
			rules = append(rules, item)
		}
		if len(rule.Domains) > 0 && len(rule.IPs) > 0 {
			add(rule.Domains, nil)
			add(nil, rule.IPs)
		} else {
			add(rule.Domains, rule.IPs)
		}
	}
	rules = append(rules, baseRules...)
	catchAll := explicitTarget(spec, spec.DefaultTarget)
	catchAll["type"], catchAll["network"] = "field", "tcp,udp"
	rules = append(rules, catchAll)
	route["rules"] = rules
	if route["domainStrategy"] == nil {
		route["domainStrategy"] = "IPIfNonMatch"
	}
	result["routing"] = route
	if len(spec.Pools) == 0 {
		data, err := json.MarshalIndent(result, "", "  ")
		return data, err
	}
	if result["observatory"] != nil && result["burstObservatory"] != nil {
		return nil, invalid("В исходном шаблоне одновременно заданы observatory и burstObservatory.", "Base template defines both observatory types.")
	}
	if result["burstObservatory"] != nil {
		return nil, invalid("Для схем leastPing выберите исходный шаблон без burstObservatory.", "Select a base without burstObservatory for leastPing schemes.")
	}
	obs, ok := result["observatory"].(map[string]any)
	if !ok && result["observatory"] != nil {
		return nil, invalid("Некорректная observatory исходного шаблона.", "Invalid base observatory.")
	}
	if obs == nil {
		obs = map[string]any{"probeURL": "https://www.gstatic.com/generate_204", "probeInterval": "30s", "enableConcurrency": true}
	}
	if old, ok := obs["subjectSelector"].([]any); ok {
		subjects = append(old, subjects...)
	}
	obs["subjectSelector"] = subjects
	result["observatory"] = obs
	data, err := json.MarshalIndent(result, "", "  ")
	return data, err
}

// Summary omits the base JSON and all credentials.
func Summary(spec services.RoutingSpec) string {
	groups := []string{}
	for _, pool := range spec.Pools {
		groups = append(groups, fmt.Sprintf("%s: %d", pool.Name, len(pool.Hosts)))
	}
	sort.Strings(groups)
	return strings.Join(groups, " · ")
}
