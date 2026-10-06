// SPDX-License-Identifier: AGPL-3.0-only

// Package v1 defines the stable HTTP/JSON boundary between the AGPL Core and
// an independently deployed Netriun Nexus Enterprise service. It contains DTOs
// only; it does not link proprietary implementation code into Core.
package v1

const (
	MajorVersion = 1
	BasePath     = "/enterprise/v1"
)

type ServiceInfo struct {
	Product        string   `json:"product"`
	ServiceVersion string   `json:"service_version"`
	APIMajors      []int    `json:"api_majors"`
	Capabilities   []string `json:"capabilities"`
}

type InstallationContext struct {
	InstallationID string `json:"installation_id"`
	WorkspaceID    int64  `json:"workspace_id"`
	RequestID      string `json:"request_id"`
}

type CapabilityRequest struct {
	Context    InstallationContext `json:"context"`
	Capability string              `json:"capability"`
	Operation  string              `json:"operation"`
	Input      map[string]any      `json:"input,omitempty"`
}

type CapabilityResponse struct {
	Output map[string]any `json:"output,omitempty"`
	Error  *Error         `json:"error,omitempty"`
}

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
