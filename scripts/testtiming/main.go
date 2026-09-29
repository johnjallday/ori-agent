// testtiming summarizes go test -json without rerunning tests. It reads stdin;
// the orchestration wrapper owns raw artifacts and the real test exit status.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

const maxRecord = 16 << 20
const tailLimit = 16 << 10
const diagnosticLimit = 20

type event struct {
	Action, Package, ImportPath, Test, Output string
	Elapsed                                   float64
}

type testResult struct {
	Name, Status string
	Elapsed      float64
}

type packageResult struct {
	Name, Status string
	Started      bool
	Cached       bool
	Invalid      bool
	Elapsed      float64
	Active       map[string]string // bounded output tail for each active test
	Tests        []testResult
	Tail         string
	Failures     []string
	FailedTests  int
}

type report struct {
	Packages    map[string]*packageResult
	Builds      map[string]string
	Problems    []string
	ProblemN    int
	BuildFailed bool
}

func (r *report) problem(message string) {
	r.ProblemN++
	if len(r.Problems) < 10 {
		r.Problems = append(r.Problems, message)
	}
}

func (r *report) pkg(name string) *packageResult {
	p := r.Packages[name]
	if p == nil {
		p = &packageResult{Name: name, Active: make(map[string]string)}
		r.Packages[name] = p
	}
	return p
}

func tail(previous, next string) string {
	if len(next) >= tailLimit {
		return next[len(next)-tailLimit:]
	}
	if len(previous)+len(next) > tailLimit {
		previous = previous[len(previous)+len(next)-tailLimit:]
	}
	return previous + next
}

func scan(input io.Reader, consume func(int, string)) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), maxRecord)
	line := 0
	for scanner.Scan() {
		line++
		consume(line, scanner.Text())
	}
	return scanner.Err()
}

func summarize(input io.Reader) *report {
	r := &report{Packages: make(map[string]*packageResult), Builds: make(map[string]string)}
	err := scan(input, func(line int, text string) {
		var e event
		if err := json.Unmarshal([]byte(text), &e); err != nil || e.Action == "" || e.Elapsed < 0 {
			r.problem(fmt.Sprintf("Invalid JSON event at line %d", line))
			return
		}
		if e.Action == "build-output" || e.Action == "build-fail" {
			name := e.ImportPath
			if name == "" {
				name = e.Package
			}
			if e.Action == "build-fail" {
				r.BuildFailed = true
			}
			r.Builds[name] = tail(r.Builds[name], e.Output)
			return
		}
		if e.Package == "" {
			r.problem(fmt.Sprintf("Missing package at line %d", line))
			return
		}
		p := r.pkg(e.Package)
		if e.Action == "output" {
			p.Tail = tail(p.Tail, e.Output)
			if _, active := p.Active[e.Test]; active {
				p.Active[e.Test] = tail(p.Active[e.Test], e.Output)
			}
			fields := strings.Fields(e.Output)
			if e.Test == "" && len(fields) >= 3 && fields[0] == "ok" && fields[1] == e.Package && fields[2] == "(cached)" {
				p.Cached = true
			}
			return
		}
		if e.Test != "" {
			switch e.Action {
			case "run":
				if _, active := p.Active[e.Test]; active {
					p.Invalid = true
					r.problem("Duplicate test start: " + e.Package + "/" + e.Test)
				}
				p.Active[e.Test] = ""
			case "pass", "fail", "skip":
				output, active := p.Active[e.Test]
				if !active {
					p.Invalid = true
					r.problem("Test completion without start: " + e.Package + "/" + e.Test)
				}
				p.Tests = append(p.Tests, testResult{e.Test, e.Action, e.Elapsed})
				if e.Action == "fail" {
					p.FailedTests++
					if len(p.Failures) < diagnosticLimit {
						p.Failures = append(p.Failures, e.Test+"\n"+output)
					}
				}
				delete(p.Active, e.Test)
			}
			return // pause/cont and future fields/actions do not imply completion.
		}
		switch e.Action {
		case "start":
			if p.Started {
				p.Invalid = true
				r.problem("Duplicate package start: " + e.Package)
			}
			p.Started = true
		case "pass", "fail", "skip":
			if p.Status != "" {
				p.Invalid = true
				r.problem("Duplicate package completion: " + e.Package)
			}
			p.Status, p.Elapsed = e.Action, e.Elapsed
			if e.Action == "pass" && (!p.Started || p.Invalid || p.FailedTests != 0 || len(p.Active) != 0) {
				p.Status = "incomplete"
				r.problem("Inconsistent package pass: " + e.Package)
			}
		}
	})
	if err != nil {
		r.problem("Unreadable/truncated JSON stream (16 MiB record limit): " + err.Error())
	}
	if len(r.Packages) == 0 && !r.BuildFailed {
		r.problem("No package results received")
	}
	names := make([]string, 0, len(r.Packages))
	for name := range r.Packages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := r.Packages[name]
		if p.Status == "" {
			p.Status = "incomplete"
			r.problem("Missing package completion: " + p.Name)
		}
		if p.Status != "pass" {
			p.Cached = false
		}
	}
	sort.Strings(r.Problems)
	return r
}

func escaped(s string) string {
	// Quote controls/newlines before escaping HTML and Markdown table delimiters.
	safe := html.EscapeString(strconv.Quote(s))
	safe = strings.NewReplacer("|", "&#124;", "::", "&#58;&#58;", "##[", "&#35;&#35;[").Replace(safe)
	return "<code>" + safe + "</code>"
}

func (r *report) failed() bool {
	if r.BuildFailed || r.ProblemN > 0 {
		return true
	}
	for _, p := range r.Packages {
		if p.Status == "fail" || p.Status == "incomplete" || p.FailedTests != 0 {
			return true
		}
	}
	return false
}

func (r *report) markdown() string {
	var b strings.Builder
	b.WriteString("## Unit test timings\n\n")
	packages := make([]*packageResult, 0, len(r.Packages))
	counts := map[string]int{}
	top, nested := map[string]int{}, map[string]int{}
	type timedTest struct {
		Package string
		testResult
	}
	var topTimes, nestedTimes []timedTest
	for _, p := range r.Packages {
		packages = append(packages, p)
		status := p.Status
		if p.Cached && status == "pass" {
			status = "cached"
		}
		counts[status]++
		if p.Cached {
			continue
		} // Replayed events are not fresh execution.
		for _, test := range p.Tests {
			if strings.Contains(test.Name, "/") {
				nested[test.Status]++
				nestedTimes = append(nestedTimes, timedTest{p.Name, test})
			} else {
				top[test.Status]++
				topTimes = append(topTimes, timedTest{p.Name, test})
			}
		}
	}
	_, _ = fmt.Fprintf(&b, "Packages: executed/pass **%d**, cached **%d**, skipped **%d**, failed **%d**, incomplete **%d**. Build failure: **%t**. Stream problems: **%d**.\n\n",
		counts["pass"], counts["cached"], counts["skip"], counts["fail"], counts["incomplete"], r.BuildFailed, r.ProblemN)
	_, _ = fmt.Fprintf(&b, "Fresh top-level completions: pass %d / fail %d / skip %d. Nested: pass %d / fail %d / skip %d.\n\n", top["pass"], top["fail"], top["skip"], nested["pass"], nested["fail"], nested["skip"])
	b.WriteString("Package durations overlap; **do not sum them into job time**. Cached timings are excluded. Parent and nested test timings are separate and not additive. Compilation, setup and cache transfer are outside these test timings.\n\n")
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Elapsed != packages[j].Elapsed {
			return packages[i].Elapsed > packages[j].Elapsed
		}
		return packages[i].Name < packages[j].Name
	})
	b.WriteString("### Slow packages\n\n| Package | Status | Seconds |\n|---|---|---:|\n")
	shown := 0
	for _, p := range packages {
		if p.Cached {
			continue
		}
		_, _ = fmt.Fprintf(&b, "| %s | %s | %.3f |\n", escaped(p.Name), p.Status, p.Elapsed)
		shown++
		if shown == 10 {
			break
		}
	}
	for _, group := range []struct {
		name  string
		tests []timedTest
	}{{"Slow top-level tests", topTimes}, {"Slow nested tests (not additive)", nestedTimes}} {
		sort.Slice(group.tests, func(i, j int) bool {
			a, z := group.tests[i], group.tests[j]
			if a.Elapsed != z.Elapsed {
				return a.Elapsed > z.Elapsed
			}
			if a.Package != z.Package {
				return a.Package < z.Package
			}
			return a.Name < z.Name
		})
		_, _ = fmt.Fprintf(&b, "\n### %s\n\n| Test | Status | Seconds |\n|---|---|---:|\n", group.name)
		for i, test := range group.tests {
			if i == 10 {
				break
			}
			_, _ = fmt.Fprintf(&b, "| %s | %s | %.3f |\n", escaped(test.Package+"/"+test.Name), test.Status, test.Elapsed)
		}
	}
	for _, problem := range r.Problems {
		_, _ = fmt.Fprintf(&b, "\n- %s\n", escaped(problem))
	}
	return b.String()
}

func diagnostics(w io.Writer, text string) error {
	// Quote controls AND break both workflow command syntaxes anywhere in a
	// line: a log prefix alone is not a sufficient command-injection boundary.
	commands := strings.NewReplacer("::", `\x3a\x3a`, "##[", `\x23\x23[`)
	for _, line := range strings.Split(text, "\n") {
		quoted := commands.Replace(strconv.Quote(line))
		if _, err := fmt.Fprintf(w, "test output: %s\n", quoted); err != nil {
			return err
		}
	}
	return nil
}

func run(input io.Reader, output, stderr io.Writer) int {
	r := summarize(input)
	if _, err := io.WriteString(output, r.markdown()); err != nil {
		return 1
	}
	if !r.failed() {
		return 0
	}
	names := make([]string, 0, len(r.Packages))
	for name := range r.Packages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := r.Packages[name]
		if p.Status != "fail" && p.Status != "incomplete" && p.FailedTests == 0 {
			continue
		}
		if err := diagnostics(stderr, name+" (bounded tails; full output in raw JSON artifact)\n"+strings.Join(p.Failures, "\n")+"\n"+p.Tail); err != nil {
			return 1
		}
	}
	names = names[:0]
	for name := range r.Builds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := diagnostics(stderr, "build "+name+"\n"+r.Builds[name]); err != nil {
			return 1
		}
	}
	return 1
}

func main() {
	stderrMode := flag.Bool("stderr", false, "quote raw compiler/stderr diagnostics instead of parsing JSON")
	flag.Parse()
	if *stderrMode {
		writeFailed := false
		err := scan(os.Stdin, func(_ int, line string) {
			if diagnostics(os.Stdout, line) != nil {
				writeFailed = true
			}
		})
		if err != nil || writeFailed {
			os.Exit(1)
		}
		return
	}
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}
