package main

import (
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/widget"
	"fyne.io/fyne/v2/container"
)

func main() {
	log.Println("Starting Dolphin Desktop...")

	// Create Fyne app
	myApp := app.NewWithID("com.dolphin.desktop")
	myWindow := myApp.NewWindow("Dolphin Agent Desktop")
	myWindow.Resize(fyne.NewSize(800, 600))

	// Simple UI for now
	title := widget.NewLabel("Dolphin Agent Desktop")
	content := widget.NewLabel("Desktop UI coming soon...")

	layout := container.NewVBox(title, content)
	myWindow.SetContent(layout)

	myWindow.ShowAndRun()
}