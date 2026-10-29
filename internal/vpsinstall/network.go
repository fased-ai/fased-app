package vpsinstall

import "time"

// NetworkApproval is an explicit owner review of private UI and console recovery.
// It is never inferred from an app session, installation, or saved preference.
type NetworkApproval struct {
	PrivateUI       bool `json:"privateUi"`
	ConsoleRecovery bool `json:"consoleRecovery"`
}
type NetworkStatus struct {
	Phase     string    `json:"phase"`
	AttemptID string    `json:"attemptId,omitempty"`
	Deadline  time.Time `json:"deadline,omitzero"`
}
