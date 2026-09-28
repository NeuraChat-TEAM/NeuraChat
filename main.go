package main

import (
	"embed"
	"log"

	"neura/internal/application"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app, err := application.NewApp()
	if err != nil {
		log.Fatalf("neura: не удалось инициализировать: %v", err)
	}

	err = wails.Run(&options.App{
		Title:            "Neura",
		Width:            1180,
		Height:           820,
		MinWidth:         880,
		MinHeight:        600,
		Frameless:        true,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 25, G: 25, B: 25, A: 1},
		OnStartup:        app.OnStartup,
		OnShutdown:       app.OnShutdown,
		Bind:             []interface{}{app},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
	})
	if err != nil {
		log.Fatalf("neura: %v", err)
	}
}
