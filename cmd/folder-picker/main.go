package main

import (
	"embed"
	"flag"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed frontend/*
var assets embed.FS

func main() {
	// Parse flags
	workspaceID := flag.String("workspace", "", "Pre-selected workspace ID")
	flag.Parse()

	// Create an instance of the app structure
	app := NewApp(*workspaceID)

	// Create application with options
	err := wails.Run(&options.App{
		Title:             "Add Directory - Ori Agent",
		Width:             450,
		Height:            350,
		MinWidth:          400,
		MinHeight:         300,
		MaxWidth:          600,
		MaxHeight:         500,
		HideWindowOnClose: true, // Hide instead of quit when window is closed
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarDefault(),
			Appearance: mac.NSAppearanceNameDarkAqua,
			About: &mac.AboutInfo{
				Title:   "Ori Folder Picker",
				Message: "A helper app for adding directories to Ori Agent workspaces",
			},
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
