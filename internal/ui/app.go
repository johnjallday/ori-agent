package ui

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/johnjallday/dolphin-agent/internal/store"
	"github.com/johnjallday/dolphin-agent/internal/pluginloader"
	"github.com/johnjallday/dolphin-agent/internal/config"
	"github.com/johnjallday/dolphin-agent/internal/chathttp"
	"github.com/johnjallday/dolphin-agent/internal/types"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"fyne.io/fyne/v2/container"
)

// DolphinApp wraps your existing backend with a Fyne UI
type DolphinApp struct {
	fyneApp      fyne.App
	window       fyne.Window

	// Your existing backend (UNCHANGED!)
	agentManager store.Store
	pluginLoader *pluginloader.Loader
	config       *config.Config
	chatHandler  *chathttp.Handler

	// UI components
	sidebar      *container.VBox
	chatArea     *container.VBox
	chatHistory  *container.VBox
	messageInput *widget.Entry
	sendButton   *widget.Button
	agentsList   *container.VBox
	pluginsList  *container.VBox
}

// NewDolphinApp creates a new desktop app using your existing backend
func NewDolphinApp(app fyne.App, agentManager store.Store, pluginLoader *pluginloader.Loader, cfg *config.Config) *DolphinApp {
	d := &DolphinApp{
		fyneApp:      app,
		agentManager: agentManager,
		pluginLoader: pluginLoader,
		config:       cfg,
	}

	// Initialize your existing chat handler (SAME LOGIC!)
	d.chatHandler = chathttp.NewHandler(agentManager, pluginLoader)

	// Create the window
	d.window = app.NewWindow("Dolphin Agent Desktop")
	d.window.Resize(fyne.NewSize(1200, 800))
	d.window.CenterOnScreen()

	// Build the UI
	d.createUI()

	return d
}

func (d *DolphinApp) createUI() {
	// Create main layout with sidebar and chat area
	d.sidebar = d.createSidebar()
	d.chatArea = d.createChatArea()

	// Main content with sidebar
	content := container.NewHSplit(d.sidebar, d.chatArea)
	content.SetOffset(0.3) // 30% for sidebar, 70% for chat

	d.window.SetContent(content)
}

func (d *DolphinApp) createSidebar() *container.VBox {
	sidebar := container.NewVBox()

	// Title
	title := widget.NewCard("", "", widget.NewLabel("Dolphin Desktop"))
	title.Text.TextStyle = fyne.TextStyle{Bold: true}
	sidebar.Add(title)

	// Agents section - using your existing agent logic!
	d.agentsList = container.NewVBox()
	agentsCard := widget.NewCard("Agents", "", d.agentsList)
	d.refreshAgentsList()

	// Add Agent button
	addAgentBtn := widget.NewButton("+ Add Agent", func() {
		d.showAddAgentDialog()
	})
	addAgentBtn.Importance = widget.MediumImportance

	agentsContainer := container.NewVBox(agentsCard, addAgentBtn)
	sidebar.Add(agentsContainer)

	// Plugins section - using your existing plugin logic!
	d.pluginsList = container.NewVBox()
	pluginsCard := widget.NewCard("Plugins", "", d.pluginsList)
	d.refreshPluginsList()

	sidebar.Add(pluginsCard)

	// Settings section
	settingsBtn := widget.NewButton("Settings", func() {
		d.showSettingsDialog()
	})
	settingsBtn.Importance = widget.LowImportance
	sidebar.Add(settingsBtn)

	return sidebar
}

func (d *DolphinApp) createChatArea() *container.VBox {
	chatArea := container.NewVBox()

	// Chat history (scrollable)
	d.chatHistory = container.NewVBox()
	historyScroll := container.NewScroll(d.chatHistory)
	historyScroll.SetMinSize(fyne.NewSize(400, 500))

	// Message input
	d.messageInput = widget.NewEntry()
	d.messageInput.SetPlaceHolder("Type your message here...")
	d.messageInput.MultiLine = true
	d.messageInput.Wrapping = fyne.TextWrapWord

	// Send button
	d.sendButton = widget.NewButton("Send", func() {
		d.sendMessage()
	})
	d.sendButton.Importance = widget.HighImportance

	// Handle Enter key
	d.messageInput.OnSubmitted = func(text string) {
		d.sendMessage()
	}

	// Input area
	inputArea := container.NewBorder(nil, nil, nil, d.sendButton, d.messageInput)

	// Chat area layout
	chatArea.Add(widget.NewCard("Chat", "", historyScroll))
	chatArea.Add(inputArea)

	return chatArea
}

// sendMessage uses your existing chat logic!
func (d *DolphinApp) sendMessage() {
	message := strings.TrimSpace(d.messageInput.Text)
	if message == "" {
		return
	}

	// Clear input
	d.messageInput.SetText("")

	// Add user message to UI
	d.addMessageToChat(message, true)

	// Use your existing chat handler logic (UNCHANGED!)
	go func() {
		// Create request like your web version does
		request := types.ChatRequest{
			Question: message,
		}

		// Process with your existing handler
		response, err := d.chatHandler.ProcessChatRequest(request)
		if err != nil {
			d.addMessageToChat(fmt.Sprintf("Error: %v", err), false)
			return
		}

		// Add response to chat
		d.addMessageToChat(response.Response, false)
	}()
}

func (d *DolphinApp) addMessageToChat(message string, isUser bool) {
	var messageWidget *widget.Card

	if isUser {
		messageWidget = widget.NewCard("You", "", widget.NewLabel(message))
		messageWidget.Content.(*widget.Label).Wrapping = fyne.TextWrapWord
	} else {
		messageWidget = widget.NewCard("Assistant", "", widget.NewRichTextFromMarkdown(message))
	}

	d.chatHistory.Add(messageWidget)
	d.chatHistory.Refresh()
}

// refreshAgentsList uses your existing agent management
func (d *DolphinApp) refreshAgentsList() {
	d.agentsList.RemoveAll()

	// Get agents using your existing store logic
	agents, currentAgent := d.agentManager.ListAgents()

	for _, agent := range agents {
		agentName := agent
		isActive := agent == currentAgent

		// Create agent button
		agentBtn := widget.NewButton(agent, func() {
			// Use your existing switch logic!
			err := d.agentManager.SwitchAgent(agentName)
			if err != nil {
				log.Printf("Error switching agent: %v", err)
				return
			}
			d.refreshAgentsList() // Refresh to show active state
		})

		if isActive {
			agentBtn.Importance = widget.HighImportance
		}

		d.agentsList.Add(agentBtn)
	}

	d.agentsList.Refresh()
}

// refreshPluginsList uses your existing plugin logic
func (d *DolphinApp) refreshPluginsList() {
	d.pluginsList.RemoveAll()

	// Get plugins using your existing loader
	plugins := d.pluginLoader.GetLoadedPlugins()

	for name, plugin := range plugins {
		pluginName := name
		pluginInfo := plugin

		pluginLabel := widget.NewLabel(fmt.Sprintf("%s v%s", pluginName, pluginInfo.Version))
		d.pluginsList.Add(pluginLabel)
	}

	d.pluginsList.Refresh()
}

func (d *DolphinApp) showAddAgentDialog() {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Agent name")

	dialog := widget.NewModalPopUp(
		container.NewVBox(
			widget.NewLabel("Add New Agent"),
			nameEntry,
			container.NewHBox(
				widget.NewButton("Cancel", func() {
					// Close dialog logic will be added
				}),
				widget.NewButton("Create", func() {
					name := strings.TrimSpace(nameEntry.Text)
					if name != "" {
						// Use your existing agent creation logic
						err := d.agentManager.CreateAgent(name)
						if err != nil {
							log.Printf("Error creating agent: %v", err)
							return
						}
						d.refreshAgentsList()
					}
					// Close dialog logic will be added
				}),
			),
		),
		d.window.Canvas(),
	)

	dialog.Show()
}

func (d *DolphinApp) showSettingsDialog() {
	// Settings dialog implementation
	dialog := widget.NewModalPopUp(
		widget.NewLabel("Settings coming soon..."),
		d.window.Canvas(),
	)
	dialog.Show()
}

func (d *DolphinApp) Run() {
	d.window.ShowAndRun()
}