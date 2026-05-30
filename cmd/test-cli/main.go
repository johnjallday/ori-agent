package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorWhite  = "\033[37m"
)

type TestRunner struct {
	scanner       *bufio.Scanner
	serverRunning bool
	serverPort    string
	serverURL     string
	agents        []string
}

func main() {
	runner := &TestRunner{
		scanner:    bufio.NewScanner(os.Stdin),
		serverPort: "8765",
	}

	runner.printBanner()
	runner.run()
}

func (r *TestRunner) printBanner() {
	fmt.Println(colorCyan + "╔════════════════════════════════════════════════════════════╗" + colorReset)
	fmt.Println(colorCyan + "║         Ori Agent - Interactive Testing CLI               ║" + colorReset)
	fmt.Println(colorCyan + "╚════════════════════════════════════════════════════════════╝" + colorReset)
	fmt.Println()
}

func (r *TestRunner) run() {
	for {
		r.printMenu()
		choice := r.prompt(colorYellow + "\nSelect option" + colorReset)

		switch choice {
		case "1":
			r.checkEnvironment()
		case "2":
			r.buildServer()
		case "3":
			r.startServer()
		case "4":
			r.stopServer()
		case "5":
			r.runQuickTest()
		case "6":
			r.runAllTests()
		case "7":
			r.interactiveChat()
		case "8":
			r.viewLogs()
		case "9":
			r.cleanupTestData()
		case "h", "help":
			r.printHelp()
		case "q", "quit", "exit":
			r.cleanup()
			fmt.Println(colorGreen + "\n👋 Goodbye!" + colorReset)
			return
		default:
			fmt.Println(colorRed + "Invalid option. Type 'h' for help." + colorReset)
		}

		fmt.Println() // Spacing between operations
	}
}

func (r *TestRunner) printMenu() {
	fmt.Println(colorBlue + "═══════════════════════════════════════" + colorReset)
	fmt.Println(colorWhite + "Setup & Environment" + colorReset)
	fmt.Println("  1. Check environment")
	fmt.Println("  2. Build server")
	fmt.Println("  3. Start server")
	fmt.Println("  4. Stop server")
	fmt.Println()
	fmt.Println(colorWhite + "Testing" + colorReset)
	fmt.Println("  5. Quick test (health check)")
	fmt.Println("  6. Run all automated tests")
	fmt.Println("  7. Interactive chat test")
	fmt.Println()
	fmt.Println(colorWhite + "Utilities" + colorReset)
	fmt.Println("  8. View logs")
	fmt.Println("  9. Cleanup test data")
	fmt.Println()
	fmt.Println("  h. Help    q. Quit")
	fmt.Println(colorBlue + "═══════════════════════════════════════" + colorReset)
}

func (r *TestRunner) checkEnvironment() {
	fmt.Println(colorCyan + "\n🔍 Checking environment..." + colorReset)

	checks := []struct {
		name  string
		check func() (bool, string)
	}{
		{"Go installed", r.checkGo},
		{"Server binary", r.checkServerBinary},
		{"API key set", r.checkAPIKey},
		{"Port available", r.checkPort},
	}

	allPassed := true
	for _, c := range checks {
		passed, msg := c.check()
		if passed {
			fmt.Printf("  %s ✓ %s%s\n", colorGreen, c.name, colorReset)
			if msg != "" {
				fmt.Printf("    %s\n", msg)
			}
		} else {
			fmt.Printf("  %s ✗ %s%s\n", colorRed, c.name, colorReset)
			if msg != "" {
				fmt.Printf("    %s\n", msg)
			}
			allPassed = false
		}
	}

	if allPassed {
		fmt.Println(colorGreen + "\n✓ Environment ready!" + colorReset)
	} else {
		fmt.Println(colorYellow + "\n⚠ Some checks failed. See messages above." + colorReset)
	}
}

func (r *TestRunner) buildServer() {
	fmt.Println(colorCyan + "\n🔨 Building server..." + colorReset)

	cmd := exec.Command("go", "build", "-o", "bin/ori-agent", "./cmd/server")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("%s✗ Build failed: %v%s\n", colorRed, err, colorReset)
		return
	}

	fmt.Println(colorGreen + "✓ Server built successfully!" + colorReset)
}

func (r *TestRunner) startServer() {
	if r.serverRunning {
		fmt.Println(colorYellow + "⚠ Server already running" + colorReset)
		return
	}

	fmt.Println(colorCyan + "\n🚀 Starting server..." + colorReset)

	port := r.prompt("Port (default: 8765)")
	if port == "" {
		port = "8765"
	}
	r.serverPort = port
	r.serverURL = fmt.Sprintf("http://localhost:%s", port)

	// Start server in background
	cmd := exec.Command("./bin/ori-agent")
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%s", port))

	logFile, err := os.Create("test-server.log")
	if err != nil {
		fmt.Printf("%s✗ Failed to create log file: %v%s\n", colorRed, err, colorReset)
		return
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		fmt.Printf("%s✗ Failed to start server: %v%s\n", colorRed, err, colorReset)
		return
	}

	// Poll /health until the server is ready (or we give up).
	fmt.Print("Waiting for server to start")
	healthURL := fmt.Sprintf("%s/health", r.serverURL)
	client := &http.Client{Timeout: 1 * time.Second}
	const maxAttempts = 40 // 40 * 250ms = 10s
	ready := false
	for range maxAttempts {
		time.Sleep(250 * time.Millisecond)
		fmt.Print(".")
		resp, err := client.Get(healthURL)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			ready = true
			break
		}
	}
	fmt.Println()

	if !ready {
		fmt.Printf("%s✗ Server did not become ready within 10s. See test-server.log%s\n", colorRed, colorReset)
		_ = cmd.Process.Kill()
		return
	}

	r.serverRunning = true
	fmt.Printf("%s✓ Server started on %s%s\n", colorGreen, r.serverURL, colorReset)
	fmt.Println("  Logs: test-server.log")
}

func (r *TestRunner) stopServer() {
	if !r.serverRunning {
		fmt.Println(colorYellow + "⚠ Server not running" + colorReset)
		return
	}

	fmt.Println(colorCyan + "\n🛑 Stopping server..." + colorReset)

	// Kill process on port
	cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%s", r.serverPort))
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		pid := strings.TrimSpace(string(output))
		_ = exec.Command("kill", pid).Run()
		fmt.Println(colorGreen + "✓ Server stopped" + colorReset)
		r.serverRunning = false
	} else {
		fmt.Println(colorYellow + "⚠ No server process found" + colorReset)
	}
}

func (r *TestRunner) runQuickTest() {
	fmt.Println(colorCyan + "\n⚡ Running quick test..." + colorReset)

	if !r.serverRunning {
		fmt.Println(colorYellow + "⚠ Server not running. Start it first (option 3)" + colorReset)
		return
	}

	// Perform health check against the server
	healthURL := fmt.Sprintf("http://localhost:%s/health", r.serverPort)
	fmt.Printf("  Checking %s...\n", healthURL)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(healthURL)
	if err != nil {
		fmt.Printf(colorRed+"✗ Health check failed: %v\n"+colorReset, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		fmt.Println(colorGreen + "✓ Quick test passed (health check OK)" + colorReset)
	} else {
		fmt.Printf(colorRed+"✗ Health check failed: HTTP %d\n"+colorReset, resp.StatusCode)
	}
}

func (r *TestRunner) runAllTests() {
	fmt.Println(colorCyan + "\n🧪 Running all automated tests..." + colorReset)

	confirm := r.prompt("This will run all Go tests. Continue? (y/n)")
	if strings.ToLower(confirm) != "y" {
		fmt.Println("Cancelled.")
		return
	}

	fmt.Println("\n" + colorBlue + "Running tests..." + colorReset)

	cmd := exec.Command("go", "test", "./tests/user/...", "-v")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("\n%s✗ Some tests failed%s\n", colorRed, colorReset)
		return
	}

	fmt.Println(colorGreen + "\n✓ All tests passed!" + colorReset)
}

func (r *TestRunner) interactiveChat() {
	fmt.Println(colorCyan + "\n💬 Interactive Chat Test" + colorReset)

	if !r.serverRunning {
		fmt.Println(colorYellow + "⚠ Server not running. Start it first (option 3)" + colorReset)
		return
	}

	agentName := r.prompt("Agent name")
	if agentName == "" {
		fmt.Println(colorRed + "✗ Agent name required" + colorReset)
		return
	}
	model := r.prompt("Model (default: gpt-4o)")
	if model == "" {
		model = "gpt-4o"
	}

	fmt.Printf("\n%sCreating agent '%s'...%s\n", colorCyan, agentName, colorReset)
	if err := r.apiCreateAgent(agentName, model); err != nil {
		fmt.Printf("%s✗ Failed to create agent: %v%s\n", colorRed, err, colorReset)
		return
	}

	r.agents = append(r.agents, agentName)
	fmt.Println(colorGreen + "✓ Agent created!" + colorReset)
	fmt.Println("\nType messages (or 'exit' to return to menu):")

	for {
		msg := r.prompt(colorPurple + "You" + colorReset)
		if msg == "exit" {
			break
		}
		if msg == "" {
			continue
		}

		reply, err := r.apiSendChat(agentName, msg)
		if err != nil {
			fmt.Printf("%s✗ Chat failed: %v%s\n", colorRed, err, colorReset)
			continue
		}
		fmt.Printf("%s%s:%s %s\n", colorCyan, agentName, colorReset, reply)
	}
}

// apiCreateAgent creates an agent via POST /api/agents.
// The server defaults missing fields (provider, system prompt, etc.) based on
// global settings, so we only send the minimum needed to drive a chat round.
func (r *TestRunner) apiCreateAgent(name, model string) error {
	body, err := json.Marshal(map[string]any{
		"name":        name,
		"type":        "tool-calling",
		"model":       model,
		"description": "Created by test-cli",
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, r.serverURL+"/api/agents", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}
	return nil
}

// apiSendChat posts a chat turn to /api/chat and returns the reply text.
// We only surface the "response" field; the chat API returns extra metadata
// (route info, action plans, etc.) that the CLI doesn't need to render.
func (r *TestRunner) apiSendChat(agentName, message string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"question":   message,
		"agent_name": agentName,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, r.serverURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	var payload struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("%s", payload.Error)
	}
	return payload.Response, nil
}

func readErrorBody(body io.Reader) string {
	const maxBytes = 512
	buf, err := io.ReadAll(io.LimitReader(body, maxBytes))
	if err != nil {
		return "(failed to read body)"
	}
	return strings.TrimSpace(string(buf))
}

func (r *TestRunner) viewLogs() {
	fmt.Println(colorCyan + "\n📋 Recent logs" + colorReset)

	if _, err := os.Stat("test-server.log"); os.IsNotExist(err) {
		fmt.Println(colorYellow + "⚠ No log file found" + colorReset)
		return
	}

	cmd := exec.Command("tail", "-n", "20", "test-server.log")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

func (r *TestRunner) cleanupTestData() {
	fmt.Println(colorCyan + "\n🧹 Cleaning up test data..." + colorReset)

	confirm := r.prompt("Delete test agents and logs? (y/n)")
	if strings.ToLower(confirm) != "y" {
		fmt.Println("Cancelled.")
		return
	}

	// Delete test agents via API. Skip silently when the server is offline,
	// since cleanup is also useful as a "remove the log file" command.
	remaining := make([]string, 0, len(r.agents))
	for _, agent := range r.agents {
		fmt.Printf("  Deleting agent: %s\n", agent)
		if !r.serverRunning {
			fmt.Printf("    %s⚠ Server not running; agent record left in place%s\n", colorYellow, colorReset)
			remaining = append(remaining, agent)
			continue
		}
		if err := r.apiDeleteAgent(agent); err != nil {
			fmt.Printf("    %s✗ %v%s\n", colorRed, err, colorReset)
			remaining = append(remaining, agent)
		}
	}
	r.agents = remaining

	// Delete logs
	_ = os.Remove("test-server.log")

	fmt.Println(colorGreen + "✓ Cleanup complete" + colorReset)
}

// apiDeleteAgent removes an agent via DELETE /api/agents?name=...
func (r *TestRunner) apiDeleteAgent(name string) error {
	target := fmt.Sprintf("%s/api/agents?name=%s", r.serverURL, url.QueryEscape(name))
	req, err := http.NewRequest(http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, readErrorBody(resp.Body))
	}
	return nil
}

func (r *TestRunner) printHelp() {
	fmt.Println(colorCyan + "\n📖 Help" + colorReset)
	fmt.Println(`This CLI tool helps you test ori-agent interactively.

Typical workflow:
  1. Check environment (option 1)
  2. Build server (option 2)
  3. Start server (option 3)
  4. Run tests (options 5-7)
  5. Stop server (option 4)

Test Types:
  - Quick Test: Simple health check
  - All Tests: Run full automated test suite (tests/user/...)
  - Interactive Chat: Create an agent and chat with it via the HTTP API

Tips:
  - Set OPENAI_API_KEY or ANTHROPIC_API_KEY before testing
  - View logs (option 8) if tests fail
  - Clean up (option 9) between test runs to remove test agents`)
}

// Helper functions

func (r *TestRunner) checkGo() (bool, string) {
	cmd := exec.Command("go", "version")
	output, err := cmd.Output()
	if err != nil {
		return false, "Go not installed"
	}
	return true, strings.TrimSpace(string(output))
}

func (r *TestRunner) checkServerBinary() (bool, string) {
	path := "bin/ori-agent"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, "Run 'make build' or option 2"
	}
	return true, path
}

func (r *TestRunner) checkAPIKey() (bool, string) {
	if os.Getenv("OPENAI_API_KEY") != "" {
		return true, "OPENAI_API_KEY set"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return true, "ANTHROPIC_API_KEY set"
	}
	return false, "Set OPENAI_API_KEY or ANTHROPIC_API_KEY"
}

func (r *TestRunner) checkPort() (bool, string) {
	cmd := exec.Command("lsof", "-ti", ":8765")
	output, _ := cmd.Output()
	if len(output) > 0 {
		return false, "Port 8765 in use"
	}
	return true, "Port 8765 available"
}

func (r *TestRunner) prompt(message string) string {
	fmt.Printf("%s: ", message)
	r.scanner.Scan()
	return strings.TrimSpace(r.scanner.Text())
}

func (r *TestRunner) cleanup() {
	if r.serverRunning {
		r.stopServer()
	}
}
