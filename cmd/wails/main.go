package main

import (
	"context"
	"log"

	"github.com/johnjallday/dolphin-agent/internal/server"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// App struct
type App struct {
	ctx context.Context
	srv *server.Server
}

// NewApp creates a new App application struct
func NewApp(srv *server.Server) *App {
	return &App{srv: srv}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func main() {
	// Create server with all dependencies (same as web server)
	srv, err := server.New()
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	// Create application instance
	app := NewApp(srv)

	// Create application with options
	err = wails.Run(&options.App{
		Title:  "Dolphin Agent",
		Width:  1280,
		Height: 900,
		AssetServer: &assetserver.Options{
			Handler: srv.Handler(), // Use same handler as web server!
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: false,
				HideTitle:                  false,
				HideTitleBar:               false,
				FullSizeContent:            false,
				UseToolbar:                 false,
				HideToolbarSeparator:       true,
			},
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			About: &mac.AboutInfo{
				Title:   "Dolphin Agent",
				Message: "AI-powered agent with plugin system",
			},
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		log.Fatal(err)
	}
}
