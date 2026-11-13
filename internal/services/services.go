// Package services defines the boundary between the UI and system operations.
package services

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type PanelParameters struct {
	PanelDomain, SubscriptionDomain string
	Proxy, Email, Admin             string
	Protection, AuthDomain          string
	CertificateAuthority            string
	SSHPorts                        []int
	RemoteSubscription              *SubscriptionRemote `json:",omitempty"`
	Certificate                     CertificateParameters
}

// Credentials are persisted only inside the authenticated state envelope.
type CertificateParameters struct {
	Method                               string
	Token                                string `json:"-"`
	CertificateFile, KeyFile             string
	RemoteCertificateFile, RemoteKeyFile string
}

type PanelPreflighter interface {
	Preflight(context.Context, PanelParameters) (string, error)
}

type SubscriptionRemote struct {
	SSH         NodeSSH
	User, Proxy string
	Password    string `json:"-"`
}

// LivePanelInstaller adds the host-changing installation lifecycle to the demo
// interface. Confirm commits the firewall only after the operator checks SSH.
type LivePanelInstaller interface {
	PanelInstaller
	Defaults() PanelParameters
	Installed() bool
	Summary() string
	Confirm(context.Context) error
	Close() error
}
type PanelInstaller interface {
	Check(context.Context) (string, error)
	Stages() [][2]string
	Step(context.Context, int, PanelParameters) error
}

type PanelRequest struct {
	Certificate   CertificateParameters
	Action, Value string
	Protection    string
	Remote        SubscriptionRemote
	Fingerprint   string
	DeleteBackups bool
}

type PanelResult struct {
	Remote       SubscriptionRemote
	Fingerprint  string
	Installation string
	Text         string
	TextRU       string
	// Command is an interactive process, launched only after explicit selection.
	Command []string
}

type PanelManager interface {
	Manage(context.Context, PanelRequest) (PanelResult, error)
}

type Node struct {
	ConfigProfile *struct {
		UUID string `json:"activeConfigProfileUuid"`
	} `json:"configProfile"`
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	IsConnected bool   `json:"isConnected"`
	IsDisabled  bool   `json:"isDisabled"`
	XrayVersion string `json:"xrayVersion"`
	NodeVersion string `json:"nodeVersion"`
	Versions    *struct {
		Xray string `json:"xray"`
		Node string `json:"node"`
	} `json:"versions"`
	XrayUptime *json.Number `json:"xrayUptime"`
	System     *struct {
		Stats struct {
			Uptime *json.Number `json:"uptime"`
		} `json:"stats"`
	} `json:"system"`
}

func (n Node) CoreVersion() string {
	if n.Versions != nil && n.Versions.Xray != "" {
		return n.Versions.Xray
	}
	return n.XrayVersion
}

func (n Node) AgentVersion() string {
	if n.Versions != nil && n.Versions.Node != "" {
		return n.Versions.Node
	}
	return n.NodeVersion
}

type NodeSSH struct {
	Host         string
	Port         int
	IdentityFile string
	HostKey      string
}

type NodeRequest struct {
	Action, UUID, Value, Fingerprint string
	SSH                              NodeSSH
	Install                          NodeInstallParameters
	JobID                            string
	Stage                            int
}

type NodeInstallParameters struct {
	Name, Address, Domain, PanelIP                   string
	ProfileName, HostName, CountryCode, SiteTemplate string
	HostLanguage                                     string
	APIPort                                          int
	SSH                                              NodeSSH
}

// RealityProfileName expands the short name used by new node installations.
func (p NodeInstallParameters) RealityProfileName() string {
	return "VLESS_REALITY_" + strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(p.ProfileName), "VLESS_REALITY_"), " ", "_")
}

type NodeInstallStatus struct {
	Version                int
	DetailsPending         bool
	ID                     string
	Params                 NodeInstallParameters
	Stage                  int
	Completed              []bool
	NodeUUID               string
	Ready, FirewallPending bool
	LastError              string
	RollbackAt             time.Time
}

type NodeResult struct {
	Version          string
	Versions         []string
	NoChange         bool
	Nodes            []Node
	SSH              NodeSSH
	Fingerprint      string
	HostKeyChanged   bool
	NeedsSSHSettings bool
	Text, TextRU     string
	Command          []string
	Install          *NodeInstallStatus
	Jobs             []NodeInstallStatus
}

type NodeManager interface {
	ManageNodes(context.Context, NodeRequest) (NodeResult, error)
}

type ManagerRemovalPlan struct {
	Remove, Preserve []string
	Signature        string
}

type ManagerRemover interface {
	PrepareManagerRemoval(context.Context) (ManagerRemovalPlan, error)
	RemoveManager(context.Context, ManagerRemovalPlan) error
}
