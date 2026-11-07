package window

import (
	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/desktop"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"os"
	"os/exec"
)

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
	return &options.App{Title: "Fased App", Width: 1440, Height: 940, MinWidth: 390, MinHeight: 640, BackgroundColour: options.NewRGBA(15, 20, 20, 255), AssetServer: &assetserver.Options{Handler: h}, EnableDefaultContextMenu: false, HideWindowOnClose: false, DragAndDrop: &options.DragAndDrop{DisableWebViewDrop: true, EnableFileDrop: false}}
}
