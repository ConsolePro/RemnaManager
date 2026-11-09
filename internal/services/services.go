// Package services defines the boundary between the UI and system operations.
package services

import "context"

type PanelParameters struct{ PanelDomain, SubscriptionDomain string }
type PanelInstaller interface {
	Check(context.Context) (string, error)
	Stages() [][2]string
	Step(context.Context, int, PanelParameters) error
}
