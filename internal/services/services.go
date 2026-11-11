// Package services defines the boundary between the UI and system operations.
package services

import (
	"context"
	"time"
)

type PanelParameters struct {
	PanelDomain, SubscriptionDomain string
	Proxy, Email, Admin             string
	Protection, AuthDomain          string
	SSHPorts                        []int
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
	Action, Value string
	Protection    string
}

type PanelResult struct {
	Text   string
	TextRU string
	// Command is an interactive process, launched only after explicit selection.
	Command []string
}

type PanelManager interface {
	Manage(context.Context, PanelRequest) (PanelResult, error)
}

type Node struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	IsConnected bool   `json:"isConnected"`
	IsDisabled  bool   `json:"isDisabled"`
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
	Name, Address, Domain, PanelIP string
	SSH                            NodeSSH
}

type NodeInstallStatus struct {
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
	Nodes          []Node
	SSH            NodeSSH
	Fingerprint    string
	HostKeyChanged bool
	Text, TextRU   string
	Command        []string
	Install        *NodeInstallStatus
	Jobs           []NodeInstallStatus
}

type NodeManager interface {
	ManageNodes(context.Context, NodeRequest) (NodeResult, error)
}
