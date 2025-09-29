package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// App struct - minimal for API-based frontend
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// GetServerURL returns the dolphin-agent server URL
func (a *App) GetServerURL() string {
	return "http://localhost:8080"
}

// GetConfig returns the current configuration
func (a *App) GetConfig() map[string]interface{} {
	return map[string]interface{}{
		"apiKey": "",
	}
}

// SetAPIKey sets the API key
func (a *App) SetAPIKey(apiKey string) error {
	// For now, just return success - this would be implemented with proper config management
	return nil
}

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "🐬 Dolphin Desktop",
		Width:  1200,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 20, G: 25, B: 40, A: 240},
		OnStartup:        app.startup,
	})

	if err != nil {
		println("Error:", err.Error())
	}
}