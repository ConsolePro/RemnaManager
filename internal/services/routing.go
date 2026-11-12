package services

import (
	"context"
	"encoding/json"
)

// RoutingSpec is shared by client split routing, pools and server-exit groups.
// Host UUIDs are references: connection secrets remain injected by Remnawave.
type RoutingSpec struct {
	Name, BaseTemplateUUID, DefaultTarget string
	Pools                                 []RoutingPool
	Rules                                 []RoutingRule
	Exits                                 []RoutingExit
}

type RoutingExit struct {
	Name, Kind, URL, ViaPool string
	WireGuard                *RoutingWireGuard
}
type RoutingWireGuard struct {
	SecretKey, PeerPublicKey, Endpoint string
	Address                            []string
	AllowedIPs                         []string
	Reserved                           []int
	MTU                                int
}
type RoutingPool struct {
	Name  string
	Hosts []string
}
type RoutingRule struct {
	Target                  string
	Domains, IPs, Protocols []string
	Network, Port           string
}
type RoutingHost struct {
	UUID         string   `json:"uuid"`
	Remark       string   `json:"remark"`
	Address      string   `json:"address"`
	Disabled     bool     `json:"isDisabled"`
	Hidden       bool     `json:"isHidden"`
	TemplateUUID string   `json:"xrayJsonTemplateUuid"`
	Nodes        []string `json:"nodes"`
	Inbound      struct {
		Profile string `json:"configProfileUuid"`
		UUID    string `json:"configProfileInboundUuid"`
	} `json:"inbound"`
}
type RoutingTemplate struct {
	UUID   string          `json:"uuid"`
	Name   string          `json:"name"`
	Type   string          `json:"templateType"`
	Config json.RawMessage `json:"templateJson"`
}
type RoutingScheme struct {
	ID, Name, Phase, TemplateUUID string
	Spec                          RoutingSpec
}
type RoutingRequest struct {
	Action, ID string
	Spec       RoutingSpec
	Nodes      []string
}
type RoutingResult struct {
	Nodes        []Node
	Hosts        []RoutingHost
	Templates    []RoutingTemplate
	Schemes      []RoutingScheme
	Scheme       *RoutingScheme
	Text, TextRU string
}
type RoutingManager interface {
	ManageRouting(context.Context, RoutingRequest) (RoutingResult, error)
}
