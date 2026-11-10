// Package services defines the boundary between the UI and system operations.
package services

import "context"

type PanelParameters struct {
	PanelDomain, SubscriptionDomain string
	Proxy, Email, Admin             string
	SSHPorts                        []int
}

// LivePanelInstaller adds the host-changing installation lifecycle to the demo
// interface. Confirm commits the firewall only after the operator checks SSH.
type LivePanelInstaller interface {
	PanelInstaller
	Defaults() PanelParameters
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
