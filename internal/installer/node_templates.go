package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

func (r *Real) checkNodeDNS(ctx context.Context, p services.NodeInstallParameters, serverIPs ...net.IP) error {
	node, err := net.DefaultResolver.LookupIPAddr(ctx, p.Address)
	if err != nil || len(node) == 0 {
		return services.LocalizedError("Не удаётся определить IP сервера ноды. Проверьте адрес и DNS.", "Cannot resolve the node server address. Check its address and DNS.")
	}
	site, err := net.DefaultResolver.LookupIPAddr(ctx, p.Domain)
	if err != nil || len(site) == 0 {
		return services.LocalizedError("Домен сайта не разрешается. Создайте A/AAAA-запись, направленную на сервер ноды.", "The website domain does not resolve. Create A/AAAA records pointing to the node server.")
	}
	for _, address := range site {
		matched := false
		for _, server := range node {
			matched = matched || server.IP.Equal(address.IP)
		}
		for _, server := range serverIPs {
			matched = matched || server.Equal(address.IP)
		}
		if !matched {
			return services.LocalizedError("DNS сайта указывает на другой адрес: "+address.IP.String()+". Все A/AAAA-записи должны вести на эту ноду; проксирование CDN нужно отключить.", "Website DNS points to a different address: "+address.IP.String()+". All A/AAAA records must point to this node; disable CDN proxying.")
		}
	}
	return nil
}

func (r *Real) nodeInterfaceIPs(ctx context.Context, j NodeJob) ([]net.IP, error) {
	args, err := r.provisionSSHArgs(j, true)
	if err != nil {
		return nil, err
	}
	args = append(args, "root@"+j.Params.SSH.Host, "printf 'RM_NODE_IPS='; ip -j address show")
	data, err := r.Runner.Run(ctx, nil, "ssh", args...)
	if err != nil {
		return nil, services.LocalizedError("Не удалось получить IP-адреса ноды по SSH. Проверьте подключение и наличие команды ip из пакета iproute2.", "Could not read node interface IPs over SSH. Check the connection and the ip command from iproute2.")
	}
	var interfaces []struct {
		Addresses []struct {
			Local string `json:"local"`
		} `json:"addr_info"`
	}
	var payload []byte
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "RM_NODE_IPS=") {
			payload = []byte(strings.TrimPrefix(line, "RM_NODE_IPS="))
			break
		}
	}
	if err := json.Unmarshal(payload, &interfaces); err != nil {
		return nil, services.LocalizedError("Нода вернула некорректный список IP-адресов. Проверьте команду ip -j address show.", "The node returned invalid interface IP data. Check ip -j address show.")
	}
	var ips []net.IP
	for _, network := range interfaces {
		for _, address := range network.Addresses {
			if ip := net.ParseIP(address.Local); ip != nil && ip.IsGlobalUnicast() {
				ips = append(ips, ip)
			}
		}
	}
	return ips, nil
}

func nodeWebsiteArchive(name string) ([]byte, error) {
	if name == "" {
		name = "studio"
	}
	directory := "node_assets/sites/" + name
	entries, err := nodeAssets.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	zip := gzip.NewWriter(&output)
	archive := tar.NewWriter(zip)
	for _, entry := range entries {
		data, err := nodeAssets.ReadFile(directory + "/" + entry.Name())
		if err != nil {
			return nil, err
		}
		if err := archive.WriteHeader(&tar.Header{Name: entry.Name(), Mode: 0644, Size: int64(len(data)), ModTime: time.Now().UTC().Truncate(time.Second)}); err != nil {
			return nil, err
		}
		if _, err := archive.Write(data); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := zip.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

//go:embed node_assets/* node_assets/sites/*/*
var nodeAssets embed.FS

var NodeSiteTemplates = []string{"studio", "atlas", "journal"}

func nodeAPIPort(p services.NodeInstallParameters) int {
	if p.APIPort == 0 {
		return 2222
	}
	return p.APIPort
}

func nodeProfileName(j NodeJob) string {
	if j.Version >= 4 {
		p := j.Params
		p.ProfileName = p.Name
		return p.RealityProfileName()
	}
	if j.Version >= 3 {
		return j.Params.RealityProfileName()
	}
	if j.Version >= 2 && j.Params.ProfileName != "" {
		return j.Params.ProfileName
	}
	return "RM_" + j.ID[:16]
}
func nodeHostName(j NodeJob) string {
	if j.Params.HostName != "" {
		return nodeCountryHostPrefix(j) + j.Params.HostName
	}
	if j.Version >= 3 {
		return nodeCountryHostPrefix(j) + nodeProfileName(j)
	}
	return nodeCountryHostPrefix(j) + j.Params.Name
}

func nodeInboundTag(j NodeJob) string {
	if j.Version >= 3 {
		return nodeProfileName(j)
	}
	if j.Version >= 2 {
		return "VLESS_REALITY_" + strings.ReplaceAll(nodeProfileName(j), " ", "_")
	}
	return "RM_REALITY_" + j.ID[:12]
}

// ValidateNodeShortProfileName checks the panel's limit after adding the prefix.
func ValidateNodeShortProfileName(p services.NodeInstallParameters) error {
	if len(p.RealityProfileName()) > 30 {
		return services.LocalizedError("Короткое имя профиля: максимум 16 символов (30 вместе с VLESS_REALITY_).", "Short profile name: at most 16 characters (30 including VLESS_REALITY_).")
	}
	return nil
}

func validateNodeDetails(p services.NodeInstallParameters) error {
	port := nodeAPIPort(p)
	if port < 1 || port > 65535 || port == 22 || port == 80 || port == 443 || port == 8443 || port == p.SSH.Port {
		return services.LocalizedError("Порт API ноды должен быть от 1 до 65535 и отличаться от SSH, 22, 80, 443 и 8443.", "Node API port must be 1–65535 and differ from SSH, 22, 80, 443 and 8443.")
	}
	if p.ProfileName != "" && !regexp.MustCompile(`^[A-Za-z0-9_ -]{2,30}$`).MatchString(p.ProfileName) {
		return services.LocalizedError("Имя профиля: 2–30 латинских букв, цифр, пробелов, дефисов или подчёркиваний.", "Profile name must contain 2–30 Latin letters, digits, spaces, hyphens or underscores.")
	}
	if p.HostName != "" && (strings.TrimSpace(p.HostName) == "" || len(utf16.Encode([]rune(p.HostName))) > 100 || strings.ContainsAny(p.HostName, "\r\n\t")) {
		return services.LocalizedError("Название хоста: 1–100 символов без переводов строк и табуляции; эмодзи могут занимать два символа.", "Host name must contain 1–100 characters without line breaks or tabs; emoji may count as two characters.")
	}
	if p.CountryCode != "" && !ValidNodeCountry(p.CountryCode) {
		return services.LocalizedError("Выберите страну из списка панели.", "Choose a country from the panel list.")
	}
	if p.SiteTemplate != "" && p.SiteTemplate != "random" {
		found := false
		for _, name := range NodeSiteTemplates {
			found = found || name == p.SiteTemplate
		}
		if !found {
			return services.LocalizedError("Неизвестный шаблон сайта selfsteal.", "Unknown selfsteal website template.")
		}
	}
	return nil
}

func prepareNewNodeSettings(j *NodeJob) error {
	if j.Version < 2 {
		return nil
	}
	if !j.DetailsPending && j.Params.ProfileName == "" {
		if regexp.MustCompile(`^[A-Za-z0-9_ -]{2,30}$`).MatchString(j.Params.Name) && (j.Version < 3 || len(j.Params.Name) <= 16) {
			j.Params.ProfileName = j.Params.Name
		} else {
			j.Params.ProfileName = "Node_" + j.ID[:8]
		}
	}
	if j.Version >= 4 && !j.DetailsPending {
		j.Params.ProfileName = j.Params.Name
		if err := validateNodeDetails(j.Params); err != nil {
			return err
		}
		if len(utf16.Encode([]rune(nodeHostName(*j)))) > 100 {
			return services.LocalizedError("Название хоста вместе с флагом и страной превышает 100 символов.", "Host name including country and flag exceeds 100 characters.")
		}
	}
	if j.Version >= 3 && !j.DetailsPending {
		if err := ValidateNodeShortProfileName(j.Params); err != nil {
			return err
		}
	}
	if len(j.ShortIDs) == 0 {
		count, err := rand.Int(rand.Reader, big.NewInt(4))
		if err != nil {
			return err
		}
		j.ShortIDs = []string{j.ShortID}
		for len(j.ShortIDs) < int(count.Int64())+3 {
			data := make([]byte, 8)
			if _, err := rand.Read(data); err != nil {
				return err
			}
			j.ShortIDs = append(j.ShortIDs, hex.EncodeToString(data))
		}
	}
	if j.Params.SiteTemplate == "" || j.Params.SiteTemplate == "random" {
		choice, err := rand.Int(rand.Reader, big.NewInt(int64(len(NodeSiteTemplates))))
		if err != nil {
			return err
		}
		j.Params.SiteTemplate = NodeSiteTemplates[choice.Int64()]
	}
	if j.Params.APIPort == 0 {
		j.Params.APIPort = 2222
	}
	if j.Params.CountryCode == "" {
		j.Params.CountryCode = "XX"
	}
	return nil
}

func preparedRealityConfig(j NodeJob) map[string]any {
	data, _ := nodeAssets.ReadFile("node_assets/reality.json")
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		panic("invalid embedded Reality template")
	}
	inbound := config["inbounds"].([]any)[0].(map[string]any)
	inbound["tag"] = nodeInboundTag(j)
	reality := inbound["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)
	reality["serverNames"], reality["privateKey"], reality["shortIds"] = []string{j.Params.Domain}, j.RealityKey, j.ShortIDs
	return config
}
