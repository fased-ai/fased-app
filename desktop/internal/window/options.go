package window

import (
	"os"
	"os/exec"
	"runtime"

	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/appicon"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/desktop"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

// MacApplicationMenu provides standard App, Edit (Cmd+C/V/X/A/Z), and Window
// menus on macOS so clipboard shortcuts work natively in WKWebView inputs.
func MacApplicationMenu() *menu.Menu {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return menu.NewMenuFromItems(
		menu.AppMenu(),
		menu.EditMenu(),
		menu.WindowMenu(),
	)
}

// Options is shared by the shipping client and the isolated native smoke runner.
// There are no custom Go bindings or window-close hooks that control the engine.
func Options(data string) *options.App {
	h := desktop.NewHandler(data, fasedapp.UI())
	h.SetWalletOwnerOpener(func(link string) error {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(self, "--wallet-owner", link)
		if err = cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	})
	return &options.App{
		Title:            "Fased App",
		Width:            1440,
		Height:           940,
		MinWidth:         390,
		MinHeight:        640,
		BackgroundColour: options.NewRGBA(13, 13, 18, 255),
		AssetServer:      &assetserver.Options{Handler: h},
		Menu:             MacApplicationMenu(),
		// Selective context menu gating lives in web/src/desktop-runtime.ts so
		// editable inputs and selected text keep native Cut/Copy/Paste while
		// non-editable chrome stays clean.
		EnableDefaultContextMenu: true,
		HideWindowOnClose:        false,
		DragAndDrop:              &options.DragAndDrop{DisableWebViewDrop: true, EnableFileDrop: false},
		Linux: &linux.Options{
			Icon:        appicon.PNG,
			ProgramName: "fased-app",
		},
		Mac: &mac.Options{
			DisableEscapeExitsFullscreen: true,
			About: &mac.AboutInfo{
				Title:   "Fased App",
				Message: "Version " + core.Version,
				Icon:    appicon.PNG,
			},
		},
	}
}
