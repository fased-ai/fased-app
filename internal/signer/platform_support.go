package signer

import "runtime"

// PlatformSignerSupport reports host OS custody storage, kernel peer-credential IPC,
// and whether unattended/desktop live signing is eligible on this platform.
type PlatformSignerSupport struct {
	OS                         string `json:"os"`
	NativeStorageSupported     bool   `json:"nativeStorageSupported"`
	PeerCredIPCSupported       bool   `json:"peerCredIpcSupported"`
	LiveDesktopSigningEligible bool   `json:"liveDesktopSigningEligible"`
	Reason                     string `json:"reason"`
}

// CurrentPlatformSupport explicitly gates non-Linux OS custody until native
// storage and authenticated peer IPC are implemented and owner-certified.
func CurrentPlatformSupport() PlatformSignerSupport {
	if runtime.GOOS == "linux" {
		return PlatformSignerSupport{
			OS:                         runtime.GOOS,
			NativeStorageSupported:     true,
			PeerCredIPCSupported:       true,
			LiveDesktopSigningEligible: false,
			Reason:                     "Linux 3-UID openat/O_NOFOLLOW storage and SO_PEERCRED IPC supported; live execution remains disabled until explicit owner authorization",
		}
	}
	return PlatformSignerSupport{
		OS:                         runtime.GOOS,
		NativeStorageSupported:     false,
		PeerCredIPCSupported:       false,
		LiveDesktopSigningEligible: false,
		Reason:                     "native local signer storage and peer-credential IPC are not implemented on " + runtime.GOOS + "; use verified Linux remote signer or controlled rehearsal only",
	}
}
