package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorWhite  = "\033[37m"
)

type ScenarioData struct {
	Scenarios  []Scenario        `json:"scenarios"`
	Categories map[string]string `json:"categories"`
	Platforms  map[string]string `json:"platforms"`
	Difficulty map[string]string `json:"difficulty"`
}

type Scenario struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Category      string   `json:"category"`
	Platform      string   `json:"platform"`
	Difficulty    string   `json:"difficulty"`
	EstimatedTime string   `json:"estimatedTime"`
	Description   string   `json:"description"`
	Prerequisites []string `json:"prerequisites"`
	Steps         []string `json:"steps"`
	Verification  string   `json:"verification"`
	Expected      string   `json:"expected"`
	Notes         string   `json:"notes"`
}

type TestResult struct {
	ScenarioID string    `json:"scenario_id"`
	Name       string    `json:"name"`
	Passed     bool      `json:"passed"`
	Notes      string    `json:"notes"`
	Timestamp  time.Time `json:"timestamp"`
	Duration   string    `json:"duration"`
}

type TestReport struct {
	RunDate  time.Time    `json:"run_date"`
	Platform string       `json:"platform"`
	Results  []TestResult `json:"results"`
	Summary  struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"summary"`
}

func main() {
	runner := NewRunner()
	runner.Run()
}

type Runner struct {
	scanner  *bufio.Scanner
	data     *ScenarioData
	results  []TestResult
	platform string
}

func NewRunner() *Runner {
	return &Runner{
		scanner:  bufio.NewScanner(os.Stdin),
		results:  []TestResult{},
		platform: runtime.GOOS,
	}
}

func (r *Runner) Run() {
	r.printBanner()

	// Load scenarios
	if err := r.loadScenarios(); err != nil {
		fmt.Printf("%s✗ Failed to load scenarios: %v%s\n", colorRed, err, colorReset)
		return
	}

	for {
		r.printMenu()
		choice := r.prompt(colorYellow + "\nSelect option" + colorReset)

		switch choice {
		case "1":
			r.runAllScenarios()
		case "2":
			r.runByCategory()
		case "3":
			r.runSingleScenario()
		case "4":
			r.listScenarios()
		case "5":
			r.viewResults()
		case "6":
			r.exportReport()
		case "h", "help":
			r.printHelp()
		case "q", "quit", "exit":
			fmt.Println(colorGreen + "\n👋 Goodbye!" + colorReset)
			return
		default:
			fmt.Println(colorRed + "Invalid option. Type 'h' for help." + colorReset)
		}

		fmt.Println()
	}
}

func (r *Runner) printBanner() {
	fmt.Println(colorCyan + "╔════════════════════════════════════════════════════════════╗" + colorReset)
	fmt.Println(colorCyan + "║        Ori Agent - Manual Test Scenario Runner            ║" + colorReset)
	fmt.Println(colorCyan + "╚════════════════════════════════════════════════════════════╝" + colorReset)
	fmt.Println()
	fmt.Printf("Platform: %s\n", runtime.GOOS)
	fmt.Println()
}

func (r *Runner) printMenu() {
	fmt.Println(colorBlue + "═══════════════════════════════════════" + colorReset)
	fmt.Println("  1. Run all scenarios")
	fmt.Println("  2. Run scenarios by category")
	fmt.Println("  3. Run single scenario")
	fmt.Println("  4. List all scenarios")
	fmt.Println("  5. View results")
	fmt.Println("  6. Export report")
	fmt.Println()
	fmt.Println("  h. Help    q. Quit")
	fmt.Println(colorBlue + "═══════════════════════════════════════" + colorReset)
}

func (r *Runner) loadScenarios() error {
	file, err := os.Open("scenarios.json")
	if err != nil {
		// Try alternate path
		file, err = os.Open("tests/user/scenarios/scenarios.json")
		if err != nil {
			return err
		}
	}
	defer file.Close()

	r.data = &ScenarioData{}
	return json.NewDecoder(file).Decode(r.data)
}

func (r *Runner) runAllScenarios() {
	fmt.Println(colorCyan + "\n🧪 Running all scenarios..." + colorReset)

	applicable := r.getApplicableScenarios()
	if len(applicable) == 0 {
		fmt.Println(colorYellow + "No scenarios applicable for this platform" + colorReset)
		return
	}

	fmt.Printf("Found %d scenarios for platform '%s'\n\n", len(applicable), r.platform)

	for i, scenario := range applicable {
		fmt.Printf("%s[%d/%d]%s Running: %s\n", colorBlue, i+1, len(applicable), colorReset, scenario.Name)
		result := r.runScenario(&scenario)
		r.results = append(r.results, result)
		fmt.Println()
	}

	r.printSummary()
}

func (r *Runner) runByCategory() {
	fmt.Println(colorCyan + "\n📂 Select category:" + colorReset)
	fmt.Println()

	categories := make(map[string]int)
	for _, s := range r.data.Scenarios {
		if r.isApplicable(&s) {
			categories[s.Category]++
		}
	}

	i := 1
	categoryList := []string{}
	for cat, count := range categories {
		fmt.Printf("  %d. %s (%d scenarios)\n", i, cat, count)
		categoryList = append(categoryList, cat)
		i++
	}

	_ = r.prompt("\nSelect category number")
	// TODO: Parse choice and run category scenarios

	fmt.Println(colorYellow + "Category selection coming soon..." + colorReset)
}

func (r *Runner) runSingleScenario() {
	fmt.Println(colorCyan + "\n🎯 Select scenario:" + colorReset)
	fmt.Println()

	applicable := r.getApplicableScenarios()
	for i, s := range applicable {
		diffIcon := r.getDifficultyIcon(s.Difficulty)
		fmt.Printf("  %d. %s %s (%s)\n", i+1, diffIcon, s.Name, s.EstimatedTime)
	}

	_ = r.prompt("\nSelect scenario number")
	// TODO: Parse choice and run scenario

	fmt.Println(colorYellow + "Single scenario selection coming soon..." + colorReset)
}

func (r *Runner) listScenarios() {
	fmt.Println(colorCyan + "\n📋 Available Scenarios" + colorReset)
	fmt.Println()

	applicable := r.getApplicableScenarios()

	for _, s := range applicable {
		diffIcon := r.getDifficultyIcon(s.Difficulty)
		platformIcon := r.getPlatformIcon(s.Platform)

		fmt.Printf("%s%s %s %s%s\n", colorWhite, diffIcon, platformIcon, s.Name, colorReset)
		fmt.Printf("    ID: %s | Category: %s | Time: %s\n", s.ID, s.Category, s.EstimatedTime)
		fmt.Printf("    %s\n", s.Description)
		fmt.Println()
	}

	fmt.Printf("Total: %d scenarios\n", len(applicable))
}

func (r *Runner) runScenario(scenario *Scenario) TestResult {
	startTime := time.Now()

	// Print scenario header
	fmt.Println(colorCyan + "─────────────────────────────────────────" + colorReset)
	fmt.Printf("%s%s%s\n", colorWhite, scenario.Name, colorReset)
	fmt.Println(colorCyan + "─────────────────────────────────────────" + colorReset)
	fmt.Printf("Description: %s\n", scenario.Description)
	fmt.Printf("Estimated time: %s\n", scenario.EstimatedTime)
	fmt.Println()

	// Check prerequisites
	if len(scenario.Prerequisites) > 0 {
		fmt.Println(colorYellow + "Prerequisites:" + colorReset)
		for _, prereq := range scenario.Prerequisites {
			fmt.Printf("  • %s\n", prereq)
		}
		fmt.Println()

		confirm := r.prompt("Prerequisites met? (y/n)")
		if strings.ToLower(confirm) != "y" {
			return TestResult{
				ScenarioID: scenario.ID,
				Name:       scenario.Name,
				Passed:     false,
				Notes:      "Prerequisites not met",
				Timestamp:  time.Now(),
				Duration:   time.Since(startTime).String(),
			}
		}
	}

	// Execute steps
	fmt.Println(colorBlue + "Steps:" + colorReset)
	for i, step := range scenario.Steps {
		fmt.Printf("%s%d.%s %s\n", colorWhite, i+1, colorReset, step)
	}
	fmt.Println()

	// Wait for user to complete
	r.prompt(colorYellow + "Press ENTER when you've completed all steps..." + colorReset)

	// Verification
	fmt.Println()
	fmt.Printf("%sVerification:%s %s\n", colorYellow, colorReset, scenario.Verification)
	fmt.Printf("%sExpected:%s %s\n", colorGreen, colorReset, scenario.Expected)
	if scenario.Notes != "" {
		fmt.Printf("%sNotes:%s %s\n", colorCyan, colorReset, scenario.Notes)
	}
	fmt.Println()

	// Get result
	result := r.prompt(colorYellow + "Did the test pass? (y/n)" + colorReset)
	passed := strings.ToLower(result) == "y"

	notes := ""
	if !passed {
		notes = r.prompt("What went wrong? (optional)")
	}

	duration := time.Since(startTime)

	if passed {
		fmt.Printf("%s✓ PASSED%s\n", colorGreen, colorReset)
	} else {
		fmt.Printf("%s✗ FAILED%s\n", colorRed, colorReset)
	}

	return TestResult{
		ScenarioID: scenario.ID,
		Name:       scenario.Name,
		Passed:     passed,
		Notes:      notes,
		Timestamp:  time.Now(),
		Duration:   duration.String(),
	}
}

func (r *Runner) viewResults() {
	if len(r.results) == 0 {
		fmt.Println(colorYellow + "\nNo results yet. Run some scenarios first." + colorReset)
		return
	}

	fmt.Println(colorCyan + "\n📊 Test Results" + colorReset)
	fmt.Println()

	for _, result := range r.results {
		status := colorGreen + "✓ PASS" + colorReset
		if !result.Passed {
			status = colorRed + "✗ FAIL" + colorReset
		}

		fmt.Printf("%s - %s (%s)\n", status, result.Name, result.Duration)
		if result.Notes != "" {
			fmt.Printf("       Notes: %s\n", result.Notes)
		}
	}

	fmt.Println()
	r.printSummary()
}

func (r *Runner) exportReport() {
	if len(r.results) == 0 {
		fmt.Println(colorYellow + "\nNo results to export. Run some scenarios first." + colorReset)
		return
	}

	report := TestReport{
		RunDate:  time.Now(),
		Platform: r.platform,
		Results:  r.results,
	}

	// Calculate summary
	report.Summary.Total = len(r.results)
	for _, result := range r.results {
		if result.Passed {
			report.Summary.Passed++
		} else {
			report.Summary.Failed++
		}
	}

	// Create reports directory
	os.MkdirAll("../reports", 0755)

	filename := fmt.Sprintf("../reports/scenario-%s.json", time.Now().Format("2006-01-02-15-04"))
	file, err := os.Create(filename)
	if err != nil {
		fmt.Printf("%s✗ Failed to create report: %v%s\n", colorRed, err, colorReset)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Printf("%s✗ Failed to write report: %v%s\n", colorRed, err, colorReset)
		return
	}

	fmt.Printf("%s✓ Report exported: %s%s\n", colorGreen, filename, colorReset)
}

func (r *Runner) printSummary() {
	total := len(r.results)
	passed := 0
	for _, result := range r.results {
		if result.Passed {
			passed++
		}
	}
	failed := total - passed

	fmt.Println(colorCyan + "─────────────────────────────────────────" + colorReset)
	fmt.Println(colorWhite + "Summary" + colorReset)
	fmt.Println(colorCyan + "─────────────────────────────────────────" + colorReset)
	fmt.Printf("Total:  %d\n", total)
	fmt.Printf("%sPassed: %d%s\n", colorGreen, passed, colorReset)
	if failed > 0 {
		fmt.Printf("%sFailed: %d%s\n", colorRed, failed, colorReset)
	} else {
		fmt.Printf("Failed: %d\n", failed)
	}

	if total > 0 {
		passRate := float64(passed) / float64(total) * 100
		fmt.Printf("Pass rate: %.1f%%\n", passRate)
	}
}

func (r *Runner) printHelp() {
	fmt.Println(colorCyan + "\n📖 Help" + colorReset)
	fmt.Print(`
This tool guides you through manual testing scenarios for ori-agent.

Workflow:
  1. Choose scenarios to run
  2. Follow the step-by-step instructions
  3. Verify the expected outcome
  4. Report pass/fail
  5. Export results

Icons:
  🟢 Easy    🟡 Medium    🔴 Hard
  💻 All platforms    🍎 macOS only

Tips:
  - Read prerequisites carefully before starting
  - Take notes of any issues in the notes field
  - Export reports after each test session
  - Share failed test reports when filing bugs
`)
}

func (r *Runner) getApplicableScenarios() []Scenario {
	applicable := []Scenario{}
	for _, s := range r.data.Scenarios {
		if r.isApplicable(&s) {
			applicable = append(applicable, s)
		}
	}
	return applicable
}

func (r *Runner) isApplicable(s *Scenario) bool {
	return s.Platform == "all" || s.Platform == r.platform
}

func (r *Runner) getDifficultyIcon(difficulty string) string {
	switch difficulty {
	case "easy":
		return "🟢"
	case "medium":
		return "🟡"
	case "hard":
		return "🔴"
	default:
		return "⚪"
	}
}

func (r *Runner) getPlatformIcon(platform string) string {
	switch platform {
	case "macos":
		return "🍎"
	case "linux":
		return "🐧"
	case "windows":
		return "🪟"
	default:
		return "💻"
	}
}

func (r *Runner) prompt(message string) string {
	fmt.Printf("%s: ", message)
	r.scanner.Scan()
	return strings.TrimSpace(r.scanner.Text())
}
