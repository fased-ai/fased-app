package vpsinstall

import (
	"bytes"
	"testing"
)

func TestInstalledProviderFixtureRemoteIdentity(t *testing.T) {
	h, o, opt := fixtureHost()
	opt.ProviderFixture = true
	if _, err := install(t.Context(), opt, o); err == nil || len(h.calls) != 0 {
		t.Fatal("missing private origin reached OS preparation", err, h.calls)
	}
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "provider"}[enabled], func(t *testing.T) {
			h, o, opt := fixtureHost()
			opt.ProviderFixture = enabled
			if enabled {
				opt.PublicOrigin = "https://fased-test.tail123.ts.net"
			}
			m, err := install(t.Context(), opt, o)
			if err != nil || m.ProviderFixture != enabled || bytes.Contains(h.files[m.Config].Data, []byte(`"providerFixture"`)) != enabled || bytes.Contains(unit(m), []byte("--ai-fixture")) != enabled {
				t.Fatal("manifest/command mode mismatch", err)
			}
			runtime := o
			runtime.uid, runtime.euid, runtime.gid = m.AppUID, m.AppUID, m.AppGID
			if err = checkApplication(m.Config, m.Root, m.Data, m.Listen, m.PublicOrigin, runtime, enabled); err != nil {
				t.Fatal("exact installed runtime rejected", err)
			}
			if err = checkApplication(m.Config, m.Root, m.Data, m.Listen, m.PublicOrigin, runtime, !enabled); err == nil {
				t.Fatal("flag not owned by installer manifest accepted")
			}
			before := append([]byte(nil), h.files[m.Config].Data...)
			opt.ProviderFixture = !enabled
			if _, err = install(t.Context(), opt, o); err == nil {
				t.Fatal("retry silently switched provider mode")
			}
			if !bytes.Equal(before, h.files[m.Config].Data) {
				t.Fatal("refused retry changed Remote configuration")
			}
		})
	}
}
