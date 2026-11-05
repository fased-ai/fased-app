//go:build linux

package providersecrets

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

// Secret Service is the desktop OS keyring. Remote system services instead
// receive their key from systemd's encrypted credential facility. No prompts,
// helper process, plaintext-file fallback or remote network are used here.
func (Native) Key(ctx context.Context, instance string, create bool) ([]byte, error) {
	if os.Geteuid() == 0 || instance == "" {
		return nil, ErrUnavailable
	}
	if directory := os.Getenv("CREDENTIALS_DIRECTORY"); directory != "" {
		if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || !strings.HasPrefix(directory, "/run/credentials/") {
			return nil, ErrUnavailable
		}
		name := filepath.Join(directory, CredentialName)
		f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return nil, ErrUnavailable
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 32 {
			return nil, ErrUnavailable
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) && owner.Uid != 0 {
			return nil, ErrUnavailable
		}
		key := make([]byte, 32)
		if _, err = f.ReadAt(key, 0); err != nil {
			clear(key)
			return nil, ErrUnavailable
		}
		return key, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" {
		address = fmt.Sprintf("unix:path=/run/user/%d/bus", os.Geteuid())
	}
	if !strings.HasPrefix(address, "unix:") || strings.Contains(address, ";") {
		return nil, ErrUnavailable
	}
	conn, err := dbus.Connect(address)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer conn.Close()
	var owned bool
	if err = conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, "org.freedesktop.secrets").Store(&owned); err != nil || !owned {
		return nil, ErrUnavailable
	}
	service := conn.Object("org.freedesktop.secrets", "/org/freedesktop/secrets")
	attrs := map[string]string{"application": "fased", "instance": instance, "purpose": "provider-task-access"}
	var unlocked, locked []dbus.ObjectPath
	if err = service.CallWithContext(ctx, "org.freedesktop.Secret.Service.SearchItems", 0, attrs).Store(&unlocked, &locked); err != nil || len(locked) > 0 || len(unlocked) > 1 {
		return nil, ErrUnavailable
	}
	var collection dbus.ObjectPath
	if len(unlocked) == 0 {
		if err = service.CallWithContext(ctx, "org.freedesktop.Secret.Service.ReadAlias", 0, "default").Store(&collection); err != nil || !collection.IsValid() || collection == "/" {
			return nil, ErrUnavailable
		}
		var property dbus.Variant
		if err = conn.Object("org.freedesktop.secrets", collection).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.Secret.Collection", "Locked").Store(&property); err != nil {
			return nil, ErrUnavailable
		}
		locked, ok := property.Value().(bool)
		if !ok || locked {
			return nil, ErrUnavailable
		}
		if !create {
			return nil, ErrMissing
		}
	}
	var output dbus.Variant
	var session dbus.ObjectPath
	if err = service.CallWithContext(ctx, "org.freedesktop.Secret.Service.OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&output, &session); err != nil || !session.IsValid() || session == "/" {
		return nil, ErrUnavailable
	}
	defer conn.Object("org.freedesktop.secrets", session).CallWithContext(ctx, "org.freedesktop.Secret.Session.Close", 0)
	type secret struct {
		Session     dbus.ObjectPath
		Parameters  []byte
		Value       []byte
		ContentType string
	}
	if len(unlocked) == 1 {
		var value secret
		if err = conn.Object("org.freedesktop.secrets", unlocked[0]).CallWithContext(ctx, "org.freedesktop.Secret.Item.GetSecret", 0, session).Store(&value); err != nil || value.Session != session || len(value.Parameters) != 0 || len(value.Value) != 32 {
			clear(value.Value)
			return nil, ErrUnavailable
		}
		return value.Value, nil
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	properties := map[string]dbus.Variant{"org.freedesktop.Secret.Item.Label": dbus.MakeVariant("Fased background access"), "org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(attrs)}
	var item, prompt dbus.ObjectPath
	if err = conn.Object("org.freedesktop.secrets", collection).CallWithContext(ctx, "org.freedesktop.Secret.Collection.CreateItem", 0, properties, secret{session, []byte{}, key, "application/octet-stream"}, false).Store(&item, &prompt); err != nil || prompt != "/" || !item.IsValid() || item == "/" {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}
