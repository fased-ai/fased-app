package vpsinstall

type sourceSessionBinding struct {
	Version      int      `json:"version"`
	Installation Manifest `json:"installation"`
	HelperSHA256 string   `json:"helperSha256"`
}
