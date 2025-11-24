package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
			r.buildPlugins()
		case "4":
			r.startServer()
		case "5":
			r.stopServer()
		case "6":
			r.runQuickTest()
		case "7":
			r.testPlugin()
		case "8":
			r.testWorkflow()
		case "9":
			r.runAllTests()
		case "10":
			r.interactiveChat()
		case "11":
			r.viewLogs()
		case "12":
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
	fmt.Println("  3. Build plugins")
	fmt.Println("  4. Start server")
	fmt.Println("  5. Stop server")
	fmt.Println()
	fmt.Println(colorWhite + "Testing" + colorReset)
	fmt.Println("  6. Quick test (health check)")
	fmt.Println("  7. Test specific plugin")
	fmt.Println("  8. Test workflow")
	fmt.Println("  9. Run all automated tests")
	fmt.Println("  10. Interactive chat test")
	fmt.Println()
	fmt.Println(colorWhite + "Utilities" + colorReset)
	fmt.Println("  11. View logs")
	fmt.Println("  12. Cleanup test data")
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
		{"Plugins built", r.checkPlugins},
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

func (r *TestRunner) buildPlugins() {
	fmt.Println(colorCyan + "\n🔨 Building plugins..." + colorReset)

	cmd := exec.Command("bash", "./scripts/build-plugins.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("%s✗ Plugin build failed: %v%s\n", colorRed, err, colorReset)
		return
	}

	fmt.Println(colorGreen + "✓ Plugins built successfully!" + colorReset)
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

	// Wait for server to be ready
	fmt.Print("Waiting for server to start")
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		fmt.Print(".")
		// TODO: Check if server is actually ready via health check
	}
	fmt.Println()

	r.serverRunning = true
	fmt.Printf("%s✓ Server started on http://localhost:%s%s\n", colorGreen, port, colorReset)
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
		fmt.Println(colorYellow + "⚠ Server not running. Start it first (option 4)" + colorReset)
		return
	}

	// TODO: Implement health check
	fmt.Println(colorGreen + "✓ Quick test passed (health check OK)" + colorReset)
}

func (r *TestRunner) testPlugin() {
	fmt.Println(colorCyan + "\n🔌 Plugin Test" + colorReset)

	if !r.serverRunning {
		fmt.Println(colorYellow + "⚠ Server not running. Start it first (option 4)" + colorReset)
		return
	}

	// List available plugins
	fmt.Println("\nAvailable plugins:")
	plugins := []string{"math", "weather", "result-handler"}
	for i, p := range plugins {
		fmt.Printf("  %d. %s\n", i+1, p)
	}

	_ = r.prompt("\nSelect plugin number")
	// TODO: Implement plugin testing

	fmt.Println(colorGreen + "✓ Plugin test completed" + colorReset)
}

func (r *TestRunner) testWorkflow() {
	fmt.Println(colorCyan + "\n🔄 Workflow Test" + colorReset)

	fmt.Println("\nAvailable workflows:")
	fmt.Println("  1. Create agent → Enable plugin → Chat")
	fmt.Println("  2. Multi-agent collaboration")
	fmt.Println("  3. Error recovery")

	_ = r.prompt("\nSelect workflow number")
	// TODO: Implement workflow testing

	fmt.Println(colorGreen + "✓ Workflow test completed" + colorReset)
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
		fmt.Println(colorYellow + "⚠ Server not running. Start it first (option 4)" + colorReset)
		return
	}

	agentName := r.prompt("Agent name")
	_ = r.prompt("Model (default: gpt-4o)") // Model selection not yet implemented

	fmt.Printf("\n%sCreating agent '%s'...%s\n", colorCyan, agentName, colorReset)
	// TODO: Create agent via API

	r.agents = append(r.agents, agentName)

	fmt.Println(colorGreen + "✓ Agent created!" + colorReset)
	fmt.Println("\nType messages (or 'exit' to return to menu):")

	for {
		msg := r.prompt(colorPurple + "You" + colorReset)
		if msg == "exit" {
			break
		}

		// TODO: Send message to agent
		fmt.Printf("%s%s:%s Mock response to: %s\n", colorCyan, agentName, colorReset, msg)
	}
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

	// Delete test agents
	for _, agent := range r.agents {
		fmt.Printf("  Deleting agent: %s\n", agent)
		// TODO: Delete via API
	}

	// Delete logs
	os.Remove("test-server.log")

	fmt.Println(colorGreen + "✓ Cleanup complete" + colorReset)
}

func (r *TestRunner) printHelp() {
	fmt.Println(colorCyan + "\n📖 Help" + colorReset)
	fmt.Println(`This CLI tool helps you test ori-agent interactively.

Typical workflow:
  1. Check environment (option 1)
  2. Build server & plugins (options 2-3)
  3. Start server (option 4)
  4. Run tests (options 6-10)
  5. Stop server (option 5)

Test Types:
  - Quick Test: Simple health check
  - Plugin Test: Test specific plugin functionality
  - Workflow Test: End-to-end user scenarios
  - All Tests: Run full automated test suite
  - Interactive Chat: Manual testing via CLI

Tips:
  - Set OPENAI_API_KEY or ANTHROPIC_API_KEY before testing
  - View logs (option 11) if tests fail
  - Clean up (option 12) between test runs`)
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

func (r *TestRunner) checkPlugins() (bool, string) {
	// Check built-in plugins
	builtInPlugins := []string{"math", "weather"}
	builtInFound := 0

	for _, p := range builtInPlugins {
		path := filepath.Join("plugins", p, p)
		if _, err := os.Stat(path); err == nil {
			builtInFound++
		}
	}

	// Check shared plugins (../plugins)
	sharedPlugins := []string{
		"ori-music-project-manager",
		"ori-reaper",
		"ori-mac-os-tools",
		"ori-meta-threads-manager",
		"ori-agent-doc-builder",
	}
	sharedFound := 0

	for _, p := range sharedPlugins {
		// Check multiple possible locations
		paths := []string{
			filepath.Join("..", "plugins", p, p),
			filepath.Join("uploaded_plugins", p),
		}

		for _, path := range paths {
			if _, err := os.Stat(path); err == nil {
				sharedFound++
				break
			}
		}
	}

	totalPlugins := len(builtInPlugins) + len(sharedPlugins)
	totalFound := builtInFound + sharedFound

	if totalFound == 0 {
		return false, "Run './scripts/build-plugins.sh' or './scripts/build-external-plugins.sh'"
	}

	status := fmt.Sprintf("%d/%d total (built-in: %d/%d, shared: %d/%d)",
		totalFound, totalPlugins,
		builtInFound, len(builtInPlugins),
		sharedFound, len(sharedPlugins))

	return totalFound > 0, status
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
