package window

import (
	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/desktop"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// Options is shared by the shipping client and the isolated native smoke runner.
// There are no custom Go bindings or window-close hooks that control the engine.
func Options(data string) *options.App {
	return &options.App{Title: "Fased App", Width: 1440, Height: 940, MinWidth: 390, MinHeight: 640, BackgroundColour: options.NewRGBA(15, 20, 20, 255), AssetServer: &assetserver.Options{Handler: desktop.NewHandler(data, fasedapp.UI())}, EnableDefaultContextMenu: false, HideWindowOnClose: false, DragAndDrop: &options.DragAndDrop{DisableWebViewDrop: true, EnableFileDrop: false}}
}
