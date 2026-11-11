package installer

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ConsolePro/RemnaManager/internal/services"
)

//go:embed node_agent.sh
var nodeAgent []byte

type NodeJob struct {
	Version                                                 int
	DetailsPending                                          bool
	ID                                                      string
	Params                                                  services.NodeInstallParameters
	Email                                                   string
	Secret, RealityKey, ShortID, Image                      string
	ShortIDs                                                []string
	ProfileUUID, InboundUUID, NodeUUID, HostUUID, SquadUUID string
	Completed                                               map[int]bool
	CreatedAt, UpdatedAt                                    time.Time
	Ready, FirewallPending                                  bool
	RollbackAt                                              time.Time
	LastError                                               string
	UpdateID, UpdateTarget                                  string
	UpdateReinstall                                         bool
}

func NodeInstallStages() [][2]string {
	return NodeInstallStagesForVersion(2)
}
func NodeInstallStagesForVersion(version int) [][2]string {
	if version != 1 {
		return [][2]string{
			{"Проверка сервера и DNS", "Server and DNS checks"},
			{"Профиль Reality и регистрация ноды", "Reality profile and node registration"},
			{"Docker, Nginx и зависимости", "Docker, Nginx and dependencies"},
			{"SECRET_KEY и закрепление образа", "SECRET_KEY and pinned image"},
			{"Сайт selfsteal, ACME и запуск ноды", "Selfsteal website, ACME and node startup"},
			{"Хост Firefox и internal squad", "Firefox host and internal squad"},
			{"Проверка подключения к панели", "Panel connectivity check"},
			{"Firewall с таймером отката", "Firewall with rollback timer"},
			{"Подтверждение SSH-доступа", "SSH access confirmation"},
		}
	}
	return [][2]string{
		{"Проверка отдельного сервера и DNS", "Remote server and DNS checks"},
		{"Docker, Nginx и зависимости", "Docker, Nginx and dependencies"},
		{"Ключ панели и закрепление образа ноды", "Panel key and pinned node image"},
		{"Selfsteal, HTTPS и запуск ноды", "Selfsteal, HTTPS and node startup"},
		{"Профиль Reality и inbound", "Reality profile and inbound"},
		{"Нода, хост подписки и internal squad", "Node, subscription host and internal squad"},
		{"Проверка подключения к панели", "Panel connectivity check"},
		{"Firewall с таймером отката", "Firewall with rollback timer"},
		{"Подтверждение доступа по SSH", "SSH access confirmation"},
	}
}
func nodeStageID(j NodeJob, stage int) int {
	if j.Version >= 2 && stage >= 0 && stage < 9 {
		return []int{0, 4, 1, 2, 3, 5, 6, 7, 8}[stage]
	}
	return stage
}

func ValidateNodeInstall(p services.NodeInstallParameters) error {
	if err := ValidateNodeName(p.Name); err != nil {
		return err
	}
	if err := ValidateNodeSSH(p.SSH); err != nil {
		return err
	}
	if err := validateNodeDetails(p); err != nil {
		return err
	}
	if p.SSH.Port == 80 || p.SSH.Port == 443 || p.SSH.Port == 8443 {
		return services.LocalizedError("SSH-порт конфликтует с портами сервисов ноды. Выберите другой SSH-порт.", "SSH port conflicts with node services")
	}
	if net.ParseIP(p.Address) == nil && !ValidDomain(p.Address) {
		return services.LocalizedError("Адрес ноды должен быть IP-адресом или доменным именем.", "node address must be an IP address or DNS name")
	}
	if ip := net.ParseIP(p.Address); ip != nil && !ip.IsGlobalUnicast() {
		return services.LocalizedError("Укажите индивидуальный IP-адрес сервера ноды, а не широковещательный или групповой адрес.", "node address must be unicast")
	}
	if !ValidDomain(p.Domain) || net.ParseIP(p.Domain) != nil {
		return services.LocalizedError("Для selfsteal нужен домен, например cover.example.com, а не IP-адрес.", "selfsteal requires a DNS domain")
	}
	if ip := net.ParseIP(p.PanelIP); p.PanelIP != "" && (ip == nil || !ip.IsGlobalUnicast()) {
		return services.LocalizedError("Укажите IP-адрес панели, доступный с ноды, или оставьте поле пустым для автоматического определения по SSH.", "specify the panel's reachable unicast IP for the node API firewall")
	}
	return nil
}

func (r *Real) jobPath(id string) (string, error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) {
		return "", errors.New("invalid installation ID")
	}
	return filepath.Join(r.Root, "node-jobs", id+".json"), nil
}
func (r *Real) saveNodeJob(j *NodeJob) error {
	path, err := r.jobPath(j.ID)
	if err != nil {
		return err
	}
	j.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(data, '\n'), 0600)
}
func (r *Real) loadNodeJob(id string) (NodeJob, error) {
	var j NodeJob
	path, err := r.jobPath(id)
	if err != nil {
		return j, err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return j, err
	}
	if !st.Mode().IsRegular() {
		return j, errors.New("installation journal is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return j, err
	}
	if err = json.Unmarshal(data, &j); err != nil {
		return j, err
	}
	if j.ID != id || (j.Version != 1 && j.Version != 2) || j.Completed == nil || j.RealityKey == "" || len(j.ShortID) != 16 {
		return j, errors.New("invalid installation journal; refusing to regenerate keys")
	}
	key, keyErr := base64.RawURLEncoding.DecodeString(j.RealityKey)
	short, shortErr := hex.DecodeString(j.ShortID)
	if keyErr != nil || len(key) != 32 || shortErr != nil || len(short) != 8 {
		return j, errors.New("corrupt Reality keys; restore the journal instead of regenerating them")
	}
	if j.Image != "" && !regexp.MustCompile(`^remnawave/node@sha256:[a-f0-9]{64}$`).MatchString(j.Image) {
		return j, errors.New("invalid pinned node image")
	}
	if j.UpdateID != "" && !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(j.UpdateID) || j.UpdateTarget != "" && (!nodeImagePattern.MatchString(j.UpdateTarget) || j.UpdateID == "") {
		return j, errors.New("invalid node update journal")
	}
	if j.UpdateReinstall && j.UpdateID == "" {
		return j, errors.New("reinstallation mode has no saved transaction")
	}
	if j.Ready && (!j.Completed[8] || j.FirewallPending) {
		return j, errors.New("inconsistent completed installation journal")
	}
	secretStage := 2
	if j.Version >= 2 {
		secretStage = 3
	}
	if j.Completed[secretStage] && (j.Secret == "" || j.Image == "") {
		return j, errors.New("completed key/image stage has lost its secrets or pinned image")
	}
	if j.Completed[0] && j.Params.PanelIP == "" {
		return j, errors.New("started installation has lost its confirmed panel IP")
	}
	if j.Version >= 2 && (len(j.ShortIDs) < 3 || len(j.ShortIDs) > 6) {
		return j, errors.New("invalid saved Reality short IDs")
	}
	for _, value := range j.ShortIDs {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 8 {
			return j, errors.New("invalid saved Reality short ID")
		}
	}
	if j.DetailsPending {
		err = ValidateNodeSSH(j.Params.SSH)
	} else {
		err = ValidateNodeInstall(j.Params)
	}
	if err != nil {
		return j, err
	}
	return j, nil
}
func jobStatus(j NodeJob) services.NodeInstallStatus {
	completed := make([]bool, len(NodeInstallStagesForVersion(j.Version)))
	stage := len(completed)
	for i := range completed {
		completed[i] = j.Completed[i]
		if stage == len(completed) && !completed[i] {
			stage = i
		}
	}
	p := j.Params
	p.SSH.HostKey = ""
	return services.NodeInstallStatus{Version: j.Version, DetailsPending: j.DetailsPending, ID: j.ID, Params: p, Stage: stage, Completed: completed, NodeUUID: j.NodeUUID, Ready: j.Ready, FirewallPending: j.FirewallPending, LastError: j.LastError, RollbackAt: j.RollbackAt}
}
func (r *Real) pendingNodeJobs() ([]services.NodeInstallStatus, error) {
	entries, err := os.ReadDir(filepath.Join(r.Root, "node-jobs"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []services.NodeInstallStatus
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		j, e := r.loadNodeJob(strings.TrimSuffix(entry.Name(), ".json"))
		if e != nil {
			return nil, fmt.Errorf("journal %s: %w", entry.Name(), e)
		}
		if !j.Ready {
			result = append(result, jobStatus(j))
		}
	}
	return result, nil
}

func (r *Real) nodeProvision(ctx context.Context, request services.NodeRequest) (services.NodeResult, error) {
	var result services.NodeResult
	switch request.Action {
	case "node-install-connect-scan":
		return r.prepareNodeSSH(ctx, "", request.Install.SSH)
	case "node-install-pending":
		jobs, err := r.pendingNodeJobs()
		result.Jobs = jobs
		return result, err
	case "node-install-scan":
		p := request.Install
		if err := ValidateNodeInstall(p); err != nil {
			return result, err
		}
		if strings.EqualFold(p.Domain, r.state.Params.PanelDomain) || strings.EqualFold(p.Domain, r.state.Params.SubscriptionDomain) || strings.EqualFold(p.Domain, r.state.Params.AuthDomain) {
			return result, errors.New("node and panel require different domains")
		}
		return r.prepareNodeSSH(ctx, "", p.SSH)
	case "node-install-create", "node-install-connect-create":
		p := request.Install
		connect := request.Action == "node-install-connect-create"
		if connect {
			p.Name, p.Domain, p.Address = "New_node", "pending.invalid", p.SSH.Host
		}
		var validation error
		if connect {
			validation = ValidateNodeSSH(p.SSH)
		} else {
			validation = ValidateNodeInstall(p)
		}
		if validation != nil {
			return result, validation
		}
		if p.PanelIP != "" {
			p.PanelIP = net.ParseIP(p.PanelIP).String()
		}
		if ip := net.ParseIP(p.Address); ip != nil {
			p.Address = ip.String()
		}
		if ip := net.ParseIP(p.SSH.Host); ip != nil {
			p.SSH.Host = ip.String()
		}
		if strings.EqualFold(p.Domain, r.state.Params.PanelDomain) || strings.EqualFold(p.Domain, r.state.Params.SubscriptionDomain) || strings.EqualFold(p.Domain, r.state.Params.AuthDomain) {
			return result, errors.New("node and panel require different domains")
		}
		key, fingerprint, err := r.scanNodeSSH(ctx, p.SSH)
		if err != nil {
			return result, err
		}
		if request.Fingerprint != fingerprint {
			return result, errors.New("SSH fingerprint changed or was not confirmed")
		}
		p.SSH.HostKey = key
		jobs, err := r.pendingNodeJobs()
		if err != nil {
			return result, err
		}
		for _, job := range jobs {
			if job.Params.Address == p.Address || job.Params.SSH.Host == p.SSH.Host && job.Params.SSH.Port == p.SSH.Port {
				return result, fmt.Errorf("resume the existing installation %s instead of creating another", job.ID)
			}
		}
		var nodes []map[string]json.RawMessage
		if !connect {
			nodes, err = r.nodeObjects(ctx, "/nodes", "nodes")
		}
		if err != nil {
			return result, err
		}
		for _, node := range nodes {
			if rawString(node["address"]) == p.Address || rawString(node["name"]) == p.Name {
				return result, errors.New("a node with this name or address already exists in the panel")
			}
		}
		id, err := secret()
		if err != nil {
			return result, err
		}
		curve, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return result, err
		}
		short, err := secret()
		if err != nil {
			return result, err
		}
		j := NodeJob{Version: 2, DetailsPending: connect, ID: id[:32], Params: p, Email: r.state.Params.Email, RealityKey: base64.RawURLEncoding.EncodeToString(curve.Bytes()), ShortID: short[:16], Completed: map[int]bool{}, CreatedAt: time.Now().UTC()}
		if err := prepareNewNodeSettings(&j); err != nil {
			return result, err
		}
		if err = r.saveNodeJob(&j); err != nil {
			return result, err
		}
		status := jobStatus(j)
		result.Install = &status
		return result, nil
	}
	j, err := r.loadNodeJob(request.JobID)
	if err != nil {
		return result, err
	}
	status := jobStatus(j)
	result.Install = &status
	switch request.Action {
	case "node-install-details":
		if !j.DetailsPending || len(j.Completed) != 0 {
			return result, services.LocalizedError("Параметры уже сохранены. Продолжите установку через «Незавершённые установки».", "Node details are already saved. Resume from Pending installations.")
		}
		if j.Params.SSH.IdentityFile == "" {
			return result, services.LocalizedError("Сначала войдите по SSH от root и настройте ключ менеджера.", "Authenticate as root over SSH and set up the managed key first.")
		}
		p := request.Install
		p.Address, p.SSH = j.Params.Address, j.Params.SSH
		p.CountryCode = strings.ToUpper(p.CountryCode)
		if err := ValidateNodeInstall(p); err != nil {
			return result, err
		}
		if p.Domain == r.state.Params.PanelDomain || p.Domain == r.state.Params.SubscriptionDomain || p.Domain == r.state.Params.AuthDomain {
			return result, services.LocalizedError("Для ноды нужен отдельный домен.", "Use a separate node domain.")
		}
		serverIPs, err := r.nodeInterfaceIPs(ctx, j)
		if err != nil {
			return result, err
		}
		if err := r.checkNodeDNS(ctx, p, serverIPs...); err != nil {
			return result, err
		}
		nodes, err := r.nodeObjects(ctx, "/nodes", "nodes")
		if err != nil {
			return result, err
		}
		for _, node := range nodes {
			if rawString(node["name"]) == p.Name || rawString(node["address"]) == p.Address {
				return result, services.LocalizedError("Нода с этим именем или адресом уже существует в панели.", "A node with this name or address already exists.")
			}
		}
		j.Params, j.DetailsPending = p, false
		if err := prepareNewNodeSettings(&j); err != nil {
			return result, err
		}
		profiles, err := r.nodeObjects(ctx, "/config-profiles", "configProfiles", "profiles")
		if err != nil {
			return result, err
		}
		for _, profile := range profiles {
			if rawString(profile["name"]) == j.Params.ProfileName {
				return result, services.LocalizedError("Профиль с таким названием уже существует. Укажите другое имя профиля.", "A profile with this name already exists. Choose a different profile name.")
			}
		}
		if err := r.saveNodeJob(&j); err != nil {
			return result, err
		}
		status = jobStatus(j)
		result.Install = &status
		return result, nil
	case "node-install-load":
		return result, nil
	case "node-install-key":
		if err := r.installNodeManagedKey(ctx, &j); err != nil {
			return result, err
		}
		if j.NodeUUID != "" {
			if r.state.NodeSSH == nil {
				r.state.NodeSSH = map[string]services.NodeSSH{}
			}
			r.state.NodeSSH[j.NodeUUID] = j.Params.SSH
			if err := saveState(statePath(r.Root), r.state); err != nil {
				return result, err
			}
		}
		status = jobStatus(j)
		result.Install = &status
		return result, nil
	case "node-install-detect-panel-ip":
		args, e := r.provisionSSHArgs(j, true)
		if e != nil {
			return result, e
		}
		args = append(args, "root@"+j.Params.SSH.Host, "bash -s")
		data, e := r.Runner.Run(ctx, []byte("printf 'RM_SSH_CONNECTION=%s\\n' \"$SSH_CONNECTION\"\n"), "ssh", args...)
		if e != nil {
			return result, errors.New("could not determine the panel IP over SSH; enter it manually")
		}
		result.Text, e = panelIPFromSSH(data)
		return result, e
	case "node-install-panel-ip":
		if len(j.Completed) != 0 {
			return result, errors.New("cannot change the panel IP after installation stages have started")
		}
		ip := net.ParseIP(request.Install.PanelIP)
		if ip == nil || !ip.IsGlobalUnicast() {
			return result, errors.New("enter a reachable unicast panel IP")
		}
		j.Params.PanelIP = ip.String()
		if e := r.saveNodeJob(&j); e != nil {
			return result, e
		}
		status = jobStatus(j)
		result.Install = &status
		return result, nil
	case "node-install-auth":
		key, fingerprint, e := r.scanNodeSSH(ctx, j.Params.SSH)
		if e != nil {
			return result, e
		}
		if request.Fingerprint != fingerprint {
			return result, errors.New("confirm the current SSH fingerprint before authenticating")
		}
		j.Params.SSH.HostKey = key
		if e = r.saveNodeJob(&j); e != nil {
			return result, e
		}
		// An old master must never redirect a resumed job to its previous session.
		_, _ = r.Runner.Run(ctx, nil, "ssh", "-F", "/dev/null", "-S", r.nodeControl(j.ID), "-O", "exit", "root@"+j.Params.SSH.Host)
		if info, statErr := os.Lstat(r.nodeControl(j.ID)); statErr == nil {
			if info.Mode()&os.ModeSocket == 0 {
				return result, errors.New("SSH control path is not a socket")
			}
			if e = os.Remove(r.nodeControl(j.ID)); e != nil {
				return result, e
			}
		} else if !os.IsNotExist(statErr) {
			return result, statErr
		}
		args, e := r.provisionSSHArgs(j, false)
		if e != nil {
			return result, e
		}
		if j.Params.SSH.IdentityFile == "" {
			args = append(args, "-o", "PubkeyAuthentication=no", "-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "PasswordAuthentication=yes", "-o", "KbdInteractiveAuthentication=yes")
		} else {
			args = append(args, "-o", "PreferredAuthentications=publickey", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no")
		}
		args = append(args, "-M", "-N", "-f", "-o", "ControlPersist=120", "-S", r.nodeControl(j.ID), "root@"+j.Params.SSH.Host)
		result.Command = append([]string{"ssh"}, args...)
		if r.controls == nil {
			r.controls = map[string]services.NodeSSH{}
		}
		r.controls[r.nodeControl(j.ID)] = j.Params.SSH
		return result, nil
	case "node-install-rescan":
		key, fingerprint, e := r.scanNodeSSH(ctx, j.Params.SSH)
		if e != nil {
			return result, e
		}
		result.SSH = j.Params.SSH
		result.SSH.HostKey = ""
		result.Fingerprint = fingerprint
		result.HostKeyChanged = key != j.Params.SSH.HostKey
		return result, nil
	case "node-install-step":
		if request.Stage < 0 || request.Stage > 7 {
			return result, errors.New("invalid node installation stage")
		}
		err = r.runNodeStage(ctx, &j, request.Stage)
	case "node-install-confirm":
		err = r.confirmNodeInstall(ctx, &j)
	default:
		return result, errors.New("unknown node installation operation")
	}
	if err != nil {
		j.LastError = err.Error()
	} else {
		j.LastError = ""
	}
	saveErr := r.saveNodeJob(&j)
	status = jobStatus(j)
	result.Install = &status
	if err != nil {
		if j.Version >= 2 && request.Action == "node-install-step" {
			label := NodeInstallStagesForVersion(j.Version)[request.Stage]
			message := services.LocalizedError(fmt.Sprintf("Этап «%s» не завершён. Продолжение: «Незавершённые установки». Установка: %s", label[0], j.ID), fmt.Sprintf("Stage %s failed. Resume from Pending installations. Installation: %s", label[1], j.ID))
			return result, fmt.Errorf("%w: %w", message, err)
		}
		return result, fmt.Errorf("%w; installation %s is saved for resume", err, j.ID)
	}
	return result, saveErr
}

func (r *Real) nodeControl(id string) string {
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 16 {
		return filepath.Join(r.Root, "ssh", "invalid")
	}
	return filepath.Join(r.Root, "ssh", "c-"+base64.RawURLEncoding.EncodeToString(decoded))
}
func (r *Real) provisionSSHArgs(j NodeJob, batch bool) ([]string, error) {
	if err := ValidateNodeSSH(j.Params.SSH); err != nil {
		return nil, err
	}
	fields := strings.Fields(j.Params.SSH.HostKey)
	if len(fields) != 2 || fields[0] != "ssh-ed25519" {
		return nil, errors.New("missing pinned SSH host key")
	}
	if _, err := hostKeyFingerprint(fields[1]); err != nil {
		return nil, err
	}
	alias := sshIdentity(j.Params.SSH.Host, j.Params.SSH.Port)
	known := filepath.Join(r.Root, "ssh", alias, "known_hosts")
	if err := writeFile(known, []byte(alias+" "+j.Params.SSH.HostKey+"\n"), 0600); err != nil {
		return nil, err
	}
	if runtime.GOOS == "linux" && len(r.nodeControl(j.ID)) > 100 {
		return nil, errors.New("SSH control socket path is too long")
	}
	args := []string{"-F", "/dev/null", "-p", strconv.Itoa(j.Params.SSH.Port), "-o", "UserKnownHostsFile=" + known, "-o", "GlobalKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=yes", "-o", "HostKeyAlias=" + alias, "-o", "CheckHostIP=no", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "ProxyCommand=none", "-o", "ProxyJump=none", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
	if j.Params.SSH.IdentityFile != "" {
		args = append(args, "-i", j.Params.SSH.IdentityFile, "-o", "IdentitiesOnly=yes")
	}
	if batch {
		args = append(args, "-T", "-o", "BatchMode=yes", "-o", "ControlMaster=auto", "-S", r.nodeControl(j.ID))
	}
	return args, nil
}
func (r *Real) runNodeAgent(ctx context.Context, j NodeJob, stage int) ([]byte, error) {
	args, err := r.provisionSSHArgs(j, true)
	if err != nil {
		return nil, err
	}
	args = append(args, "root@"+j.Params.SSH.Host, "bash -s")
	compose, err := RenderNodeStack(j)
	if err != nil {
		return nil, err
	}
	values := map[string]string{"DOMAIN": j.Params.Domain, "PANEL_IP": j.Params.PanelIP, "EMAIL": j.Email, "COMPOSE": string(compose), "SETUP": string(setupScript), "SOURCE": j.Image}
	if j.Version >= 2 {
		site, err := nodeWebsiteArchive(j.Params.SiteTemplate)
		if err != nil {
			return nil, err
		}
		values["SITE"] = string(site)
	}
	values["API_PORT"] = strconv.Itoa(nodeAPIPort(j.Params))
	if values["SOURCE"] == "" {
		values["SOURCE"] = "remnawave/node:latest"
	}
	var input strings.Builder
	fmt.Fprintf(&input, "set +x\nRM_JOB_ID='%s'\nRM_STAGE='%d'\nRM_NODE_VERSION='%d'\n", j.ID, stage, j.Version)
	for name, value := range values {
		fmt.Fprintf(&input, "RM_%s_B64='%s'\n", name, base64.StdEncoding.EncodeToString([]byte(value)))
	}
	input.Write(nodeAgent)
	data, err := r.Runner.Run(ctx, []byte(input.String()), "ssh", args...)
	if err != nil {
		if actionErr := nodeAgentFailure(data); actionErr != nil {
			return data, fmt.Errorf("%w: %w; remote log: /var/lib/remnamanager-node/install.log", actionErr, err)
		}
		diagnostic := r.safeOutput(data)
		for _, secret := range []string{j.Secret, j.RealityKey} {
			if secret != "" {
				diagnostic = strings.ReplaceAll(diagnostic, secret, "[REDACTED]")
			}
		}
		runes := []rune(diagnostic)
		if len(runes) > 2000 {
			diagnostic = string(runes[len(runes)-2000:])
		}
		detail := strings.TrimSpace(diagnostic)
		if !strings.Contains(detail, "/var/lib/remnamanager-node/install.log") {
			detail += "; remote log: /var/lib/remnamanager-node/install.log"
		}
		return data, fmt.Errorf("%w; %s", err, detail)
	}
	return data, nil
}
func RenderNodeStack(j NodeJob) ([]byte, error) {
	image := j.Image
	if image == "" {
		image = "remnawave/node:latest"
	}
	return json.MarshalIndent(map[string]any{"name": "remnamanager-node", "services": map[string]any{"remnanode": map[string]any{"image": image, "restart": "unless-stopped", "network_mode": "host", "environment": map[string]string{"NODE_PORT": strconv.Itoa(nodeAPIPort(j.Params)), "SECRET_KEY": j.Secret}, "logging": map[string]any{"driver": "json-file", "options": map[string]string{"max-size": "10m", "max-file": "3"}}}}}, "", "  ")
}
func (r *Real) runNodeStage(ctx context.Context, j *NodeJob, stage int) error {
	if j.DetailsPending {
		return services.LocalizedError("Сначала заполните параметры ноды после входа по SSH.", "Complete node details after SSH login first.")
	}
	if ip := net.ParseIP(j.Params.PanelIP); ip == nil || !ip.IsGlobalUnicast() {
		return errors.New("confirm the panel IP before starting installation")
	}
	if stage < 0 || stage > 7 {
		return errors.New("invalid remote installation stage; firewall confirmation is separate")
	}
	if j.Ready {
		return nil
	}
	if stage > 0 && !j.Completed[stage-1] {
		return errors.New("complete the previous installation stage first")
	}
	actionStage := nodeStageID(*j, stage)
	switch actionStage {
	case 0, 1, 3:
		if _, err := r.runNodeAgent(ctx, *j, actionStage); err != nil {
			return err
		}
	case 2:
		if j.Secret == "" {
			raw, err := r.nodeAPI(ctx, "GET", "/keygen", nil)
			if err != nil {
				return err
			}
			var keys struct {
				Secret string `json:"secretKey"`
			}
			if json.Unmarshal(raw, &keys) != nil || keys.Secret == "" {
				return errors.New("panel did not return a node secret")
			}
			j.Secret = keys.Secret
			if err = r.saveNodeJob(j); err != nil {
				return err
			}
		}
		data, err := r.runNodeAgent(ctx, *j, actionStage)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "RM_NODE_IMAGE=") {
				j.Image = strings.TrimSpace(strings.TrimPrefix(line, "RM_NODE_IMAGE="))
			}
		}
		if !regexp.MustCompile(`^remnawave/node@sha256:[a-f0-9]{64}$`).MatchString(j.Image) {
			return errors.New("remote node image digest was not verified")
		}
	case 4:
		if err := r.ensureNodeProfile(ctx, j); err != nil {
			return err
		}
		if j.Version >= 2 {
			if err := r.ensureNodeRegistration(ctx, j); err != nil {
				return err
			}
		}
	case 5:
		if err := r.ensureNodeObjects(ctx, j); err != nil {
			return err
		}
	case 6:
		if err := r.waitInstalledNode(ctx, *j); err != nil {
			return err
		}
		if j.Version >= 2 {
			if err := r.waitNodeWebsite(ctx, *j); err != nil {
				return err
			}
		}
		if r.state.NodeSSH == nil {
			r.state.NodeSSH = map[string]services.NodeSSH{}
		}
		r.state.NodeSSH[j.NodeUUID] = j.Params.SSH
		if err := saveState(statePath(r.Root), r.state); err != nil {
			return err
		}
	case 7:
		data, err := r.runNodeAgent(ctx, *j, 7)
		if err != nil {
			return err
		}
		remaining := 0
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "RM_FW_REMAINING=") {
				remaining, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "RM_FW_REMAINING=")))
			}
		}
		if remaining < 1 || remaining > 120 {
			return errors.New("remote firewall did not confirm an active rollback deadline")
		}
		j.FirewallPending = true
		j.RollbackAt = time.Now().Add(time.Duration(remaining) * time.Second)
	}
	j.Completed[stage] = true
	if stage == 0 {
		j.Completed[7] = false
		j.Completed[8] = false
		j.FirewallPending = false
		j.RollbackAt = time.Time{}
	}
	return nil
}

// SSH_CONNECTION is supplied by sshd and reports the client address after NAT.
// It can differ from the backend's egress address, so the wizard confirms it.
func panelIPFromSSH(data []byte) (string, error) {
	var found string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "RM_SSH_CONNECTION=") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "RM_SSH_CONNECTION="))
		if len(fields) != 4 || found != "" {
			return "", errors.New("invalid SSH connection address; enter the panel IP manually")
		}
		ip := net.ParseIP(fields[0])
		if ip == nil || !ip.IsGlobalUnicast() {
			return "", errors.New("SSH did not report a reachable panel IP; enter it manually")
		}
		found = ip.String()
	}
	if found == "" {
		return "", errors.New("SSH connection address is unavailable; enter the panel IP manually")
	}
	return found, nil
}
func (r *Real) confirmNodeInstall(ctx context.Context, j *NodeJob) error {
	if j.Ready {
		return nil
	}
	if !j.Completed[7] || !j.FirewallPending {
		return services.LocalizedError("Сначала выполните пробное применение firewall через продолжение установки ноды.", "Run the firewall trial before confirming SSH access.")
	}
	if !j.RollbackAt.IsZero() && time.Until(j.RollbackAt) < 10*time.Second {
		return services.LocalizedError("Время подтверждения SSH истекло. Правила откатываются автоматически. Продолжите эту ноду через «Незавершённые установки», чтобы запустить новую проверку.", "SSH confirmation expired. Rules roll back automatically. Resume this node from Pending installations to start a new trial.")
	}
	if err := r.waitInstalledNode(ctx, *j); err != nil {
		return err
	}
	if _, err := r.runNodeAgent(ctx, *j, 8); err != nil {
		return err
	}
	j.FirewallPending = false
	j.RollbackAt = time.Time{}
	j.Ready = true
	j.Completed[8] = true
	return nil
}
func (r *Real) waitInstalledNode(ctx context.Context, j NodeJob) error {
	if !nodeUUIDPattern.MatchString(j.NodeUUID) {
		return errors.New("node has not been registered in the panel")
	}
	wait, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		raw, err := r.nodeAPI(wait, "GET", "/nodes/"+j.NodeUUID, nil)
		if err != nil {
			return err
		}
		var node services.Node
		if json.Unmarshal(raw, &node) == nil && node.IsConnected && !node.IsDisabled {
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("node is not connected; check panel → node TCP 2222 and resume this installation")
		case <-time.After(2 * time.Second):
		}
	}
}

func (r *Real) waitNodeWebsite(ctx context.Context, j NodeJob) error {
	wait, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	client := http.Client{Timeout: 10 * time.Second, Transport: r.Client.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	check := func() error {
		for _, scheme := range []string{"https", "http"} {
			request, err := http.NewRequestWithContext(wait, "GET", scheme+"://"+j.Params.Domain+"/", nil)
			if err != nil {
				return err
			}
			response, err := client.Do(request)
			if err != nil {
				return err
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if readErr != nil {
				return readErr
			}
			if scheme == "https" {
				if response.StatusCode != 200 || !strings.Contains(strings.ToLower(string(body)), "<html") {
					return errors.New("HTTPS did not return the prepared website")
				}
			} else {
				location, err := url.Parse(response.Header.Get("Location"))
				if err != nil || location.Scheme != "https" || location.Hostname() != j.Params.Domain || (response.StatusCode != 301 && response.StatusCode != 308) {
					return errors.New("HTTP port 80 does not redirect to the HTTPS website")
				}
			}
		}
		return nil
	}
	for {
		if err := check(); err == nil {
			return nil
		}
		select {
		case <-wait.Done():
			return services.LocalizedError("Сайт selfsteal недоступен по HTTP/HTTPS или сертификат не прошёл проверку. Проверьте A/AAAA домена, порты 80/443 и журнал Nginx на ноде. Установка сохранена для продолжения.", "Selfsteal HTTP/HTTPS or certificate verification failed. Check domain A/AAAA records, ports 80/443 and node Nginx logs. Installation is saved for resume.")
		case <-time.After(time.Second):
		}
	}
}

func rawString(raw json.RawMessage) string { var s string; json.Unmarshal(raw, &s); return s }
func (r *Real) nodeObjects(ctx context.Context, path string, keys ...string) ([]map[string]json.RawMessage, error) {
	raw, err := r.nodeAPI(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var array []map[string]json.RawMessage
	if json.Unmarshal(raw, &array) == nil {
		return array, nil
	}
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(raw, &wrapper) != nil {
		return nil, errors.New("unexpected API collection")
	}
	for _, key := range keys {
		if v, ok := wrapper[key]; ok {
			if err = json.Unmarshal(v, &array); err != nil {
				return nil, err
			}
			return array, nil
		}
	}
	return nil, errors.New("API collection shape changed; refusing to create duplicate resources")
}
func nodeRealityConfig(j NodeJob) map[string]any {
	if j.Version >= 2 {
		return preparedRealityConfig(j)
	}
	return map[string]any{
		"log":       map[string]string{"loglevel": "warning"},
		"inbounds":  []any{map[string]any{"tag": "RM_REALITY_" + j.ID[:12], "listen": "0.0.0.0", "port": 443, "protocol": "vless", "settings": map[string]any{"clients": []any{}, "decryption": "none"}, "streamSettings": map[string]any{"network": "raw", "security": "reality", "realitySettings": map[string]any{"show": false, "target": "127.0.0.1:8443", "xver": 0, "serverNames": []string{j.Params.Domain}, "privateKey": j.RealityKey, "shortIds": []string{j.ShortID}}}}},
		"outbounds": []any{map[string]string{"tag": "DIRECT", "protocol": "freedom"}, map[string]string{"tag": "BLOCK", "protocol": "blackhole"}},
		"routing":   map[string]any{"rules": []any{map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "BLOCK"}}},
	}
}
func apiUUID(raw json.RawMessage) (string, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return "", errors.New("invalid API object")
	}
	id := rawString(obj["uuid"])
	if !nodeUUIDPattern.MatchString(id) {
		return "", errors.New("API did not return a UUID")
	}
	return id, nil
}

func uuidInList(raw json.RawMessage, id string) bool {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	for _, value := range values {
		if rawString(value) == id {
			return true
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) == nil && rawString(object["uuid"]) == id {
			return true
		}
	}
	return false
}
func (r *Real) ensureNodeProfile(ctx context.Context, j *NodeJob) error {
	name := nodeProfileName(*j)
	if j.ProfileUUID == "" {
		profiles, err := r.nodeObjects(ctx, "/config-profiles", "configProfiles", "profiles")
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			if rawString(profile["name"]) == name {
				j.ProfileUUID = rawString(profile["uuid"])
				break
			}
		}
		if j.ProfileUUID == "" {
			raw, err := r.nodeAPI(ctx, "POST", "/config-profiles", map[string]any{"name": name, "config": nodeRealityConfig(*j)})
			if err != nil {
				return err
			}
			j.ProfileUUID, err = apiUUID(raw)
			if err != nil {
				return err
			}
		}
		if err = r.saveNodeJob(j); err != nil {
			return err
		}
	}
	if !nodeUUIDPattern.MatchString(j.ProfileUUID) {
		return errors.New("invalid saved profile UUID")
	}
	raw, err := r.nodeAPI(ctx, "GET", "/config-profiles/"+j.ProfileUUID, nil)
	if err != nil {
		return err
	}
	var profile struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if json.Unmarshal(raw, &profile) != nil || profile.Name != name {
		return errors.New("profile ownership changed")
	}
	var configString string
	if json.Unmarshal(profile.Config, &configString) == nil {
		profile.Config = json.RawMessage(configString)
	}
	wanted, _ := json.Marshal(nodeRealityConfig(*j))
	var actual, expected any
	json.Unmarshal(profile.Config, &actual)
	json.Unmarshal(wanted, &expected)
	// The API normalizes config formatting; compare decoded values, not bytes.
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if string(a) != string(b) {
		return errors.New("saved Reality profile was changed externally; refusing overwrite")
	}
	inbounds, err := r.nodeObjects(ctx, "/config-profiles/"+j.ProfileUUID+"/inbounds", "inbounds")
	if err != nil {
		return err
	}
	for _, inbound := range inbounds {
		if rawString(inbound["tag"]) == nodeInboundTag(*j) {
			id := rawString(inbound["uuid"])
			if !nodeUUIDPattern.MatchString(id) {
				return errors.New("invalid inbound UUID")
			}
			if j.InboundUUID != "" && j.InboundUUID != id {
				return errors.New("saved inbound changed")
			}
			j.InboundUUID = id
			return r.saveNodeJob(j)
		}
	}
	return errors.New("Reality inbound was not returned by the panel")
}
func (r *Real) ensureNodeObjects(ctx context.Context, j *NodeJob) error {
	if err := r.ensureNodeRegistration(ctx, j); err != nil {
		return err
	}
	return r.ensureNodeHostAndSquad(ctx, j)
}
func (r *Real) ensureNodeRegistration(ctx context.Context, j *NodeJob) error {
	if !nodeUUIDPattern.MatchString(j.ProfileUUID) || !nodeUUIDPattern.MatchString(j.InboundUUID) {
		return errors.New("create the Reality profile first")
	}
	nodes, err := r.nodeObjects(ctx, "/nodes", "nodes")
	if err != nil {
		return err
	}
	foundNode := false
	for _, node := range nodes {
		if rawString(node["uuid"]) == j.NodeUUID || rawString(node["name"]) == j.Params.Name {
			var config struct {
				Profile string `json:"activeConfigProfileUuid"`
			}
			json.Unmarshal(node["configProfile"], &config)
			var port int
			json.Unmarshal(node["port"], &port)
			if rawString(node["address"]) != j.Params.Address || config.Profile != j.ProfileUUID || port != nodeAPIPort(j.Params) {
				return errors.New("node name or saved node is owned by another configuration")
			}
			j.NodeUUID = rawString(node["uuid"])
			foundNode = true
			break
		}
	}
	if j.NodeUUID != "" && !foundNode {
		return errors.New("saved node was deleted externally; refusing to register a replacement")
	}
	if j.NodeUUID == "" {
		country := j.Params.CountryCode
		if country == "" {
			country = "XX"
		}
		raw, e := r.nodeAPI(ctx, "POST", "/nodes", map[string]any{"name": j.Params.Name, "address": j.Params.Address, "port": nodeAPIPort(j.Params), "countryCode": country, "configProfile": map[string]any{"activeConfigProfileUuid": j.ProfileUUID, "activeInbounds": []string{j.InboundUUID}}})
		if e != nil {
			return e
		}
		j.NodeUUID, err = apiUUID(raw)
		if err != nil {
			return err
		}
		if err = r.saveNodeJob(j); err != nil {
			return err
		}
	}
	return nil
}
func (r *Real) ensureNodeHostAndSquad(ctx context.Context, j *NodeJob) error {
	hosts, err := r.nodeObjects(ctx, "/hosts", "hosts")
	if err != nil {
		return err
	}
	foundHost := false
	for _, host := range hosts {
		var inbound struct {
			Profile string `json:"configProfileUuid"`
			Inbound string `json:"configProfileInboundUuid"`
		}
		json.Unmarshal(host["inbound"], &inbound)
		match := rawString(host["uuid"]) == j.HostUUID && j.HostUUID != "" || j.HostUUID == "" && inbound.Profile == j.ProfileUUID && inbound.Inbound == j.InboundUUID && rawString(host["address"]) == j.Params.Address
		if match {
			var port int
			json.Unmarshal(host["port"], &port)
			if rawString(host["address"]) != j.Params.Address || inbound.Profile != j.ProfileUUID || inbound.Inbound != j.InboundUUID || port != 443 || rawString(host["sni"]) != j.Params.Domain {
				return errors.New("subscription host changed externally")
			}
			if members, ok := host["nodes"]; ok && !uuidInList(members, j.NodeUUID) {
				return errors.New("subscription host no longer includes this node")
			}
			j.HostUUID = rawString(host["uuid"])
			foundHost = true
			break
		}
	}
	if j.HostUUID != "" && !foundHost {
		return errors.New("saved subscription host was deleted externally")
	}
	if j.HostUUID == "" {
		fingerprint := "chrome"
		if j.Version >= 2 {
			fingerprint = "firefox"
		}
		raw, e := r.nodeAPI(ctx, "POST", "/hosts", map[string]any{"remark": j.Params.Name, "address": j.Params.Address, "port": 443, "sni": j.Params.Domain, "fingerprint": fingerprint, "nodes": []string{j.NodeUUID}, "inbound": map[string]string{"configProfileUuid": j.ProfileUUID, "configProfileInboundUuid": j.InboundUUID}})
		if e != nil {
			return e
		}
		j.HostUUID, err = apiUUID(raw)
		if err != nil {
			return err
		}
		if err = r.saveNodeJob(j); err != nil {
			return err
		}
	}
	squads, err := r.nodeObjects(ctx, "/internal-squads", "internalSquads", "squads")
	if err != nil {
		return err
	}
	foundSquad := false
	for _, squad := range squads {
		if j.SquadUUID != "" && rawString(squad["uuid"]) == j.SquadUUID || j.SquadUUID == "" && rawString(squad["name"]) == "RM_"+j.ID[:16] {
			if rawString(squad["name"]) != "RM_"+j.ID[:16] {
				return errors.New("saved internal squad was changed externally")
			}
			j.SquadUUID = rawString(squad["uuid"])
			foundSquad = true
			break
		}
	}
	if j.SquadUUID != "" && !foundSquad {
		return errors.New("saved internal squad was deleted externally")
	}
	if j.SquadUUID == "" {
		raw, e := r.nodeAPI(ctx, "POST", "/internal-squads", map[string]any{"name": "RM_" + j.ID[:16], "inbounds": []string{j.InboundUUID}})
		if e != nil {
			return e
		}
		j.SquadUUID, err = apiUUID(raw)
		if err != nil {
			return err
		}
	}
	for _, id := range []string{j.NodeUUID, j.HostUUID, j.SquadUUID} {
		if !nodeUUIDPattern.MatchString(id) {
			return errors.New("API object has an invalid UUID")
		}
	}
	// Validate membership as well as the name before adopting an existing squad.
	for _, squad := range squads {
		if rawString(squad["uuid"]) == j.SquadUUID {
			if inbounds, ok := squad["inbounds"]; ok && !uuidInList(inbounds, j.InboundUUID) {
				return errors.New("internal squad inbounds were changed externally")
			}
		}
	}
	return r.saveNodeJob(j)
}
