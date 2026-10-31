package localinstall

import "context"

// MaintenanceRunner is an OS-owner composition dependency, never JSON/IPC
// command authority. The implementation calls only the fixed user-manager API.
type MaintenanceRunner func(context.Context, ...string) ([]byte, error)
type MaintenanceStatus struct {
	ID          string `json:"id"`
	Unit        string `json:"unit"`
	Phase       string `json:"phase"` // ready/running/finished/failed; not an update receipt.
	Persistent  bool   `json:"persistent"`
	Recoverable bool   `json:"recoverable"`
	UpdatePhase string `json:"updatePhase,omitempty"`
}
