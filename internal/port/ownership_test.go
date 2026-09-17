package port

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLookupOutputStopsAtTheTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the POSIX sleep binary")
	}
	previous := ownerLookupTimeout
	ownerLookupTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ownerLookupTimeout = previous })

	started := time.Now()
	if _, err := lookupOutput("sleep", "30"); err == nil {
		t.Fatal("expected a lookup that outlives its timeout to fail")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("lookup returned after %s; the timeout did not stop it", elapsed)
	}
}

// TestHelperHoldLoopbackListener is not a test: the next test runs it in a
// child process to hold 127.0.0.1:<port> the way a stale server would.
func TestHelperHoldLoopbackListener(t *testing.T) {
	if os.Getenv("ORI_PORT_TEST_HOLD_LOOPBACK") != "1" {
		t.Skip("helper process only")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	_, _ = os.Stdout.WriteString("PORT " + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port) + "\n")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// A wildcard bind succeeds beside another process's loopback listener on both
// macOS and Windows, so only the owner lookup can find a stale server.
func TestFindPortProcessesFindsAnotherProcessLoopbackListener(t *testing.T) {
	tool := map[string]string{"darwin": "lsof", "linux": "lsof", "windows": "netstat"}[runtime.GOOS]
	if tool == "" {
		t.Skip("no owner lookup on this platform")
	}
	if _, err := exec.LookPath(tool); err != nil {
		t.Skipf("%s is not installed", tool)
	}
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperHoldLoopbackListener$")
	helper.Env = append(os.Environ(), "ORI_PORT_TEST_HOLD_LOOPBACK=1")
	stdin, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = helper.Wait()
	})

	port := 0
	scanner := bufio.NewScanner(stdout)
	for port == 0 && scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "PORT "); ok {
			port, _ = strconv.Atoi(value)
		}
	}
	if port == 0 {
		t.Fatal("helper process did not report its port")
	}
	processes, err := FindPortProcesses(port)
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		if process.PID == helper.Process.Pid {
			if process.Name == "" {
				t.Fatalf("found pid %d on port %d but resolved no process name", process.PID, port)
			}
			return
		}
	}
	t.Fatalf("owner lookup for port %d = %+v; want the listening helper pid %d", port, processes, helper.Process.Pid)
}

func TestParseNetstatListeners(t *testing.T) {
	output := []byte(`
Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1012
  TCP    127.0.0.1:8765         0.0.0.0:0              LISTENING       4242
  TCP    127.0.0.1:8765         127.0.0.1:50123        ESTABLISHED     4242
  TCP    127.0.0.1:50123        127.0.0.1:8765         ESTABLISHED     7777
  TCP    0.0.0.0:18765          0.0.0.0:0              LISTENING       5151
  TCP    [::]:8765              [::]:0                 ABHÖREN         4343
  TCP    [fe80::1%4]:8765       [::]:0                 EN ÉCOUTE       4444
  UDP    0.0.0.0:8765           *:*                                    9999
`)
	got := parseNetstatListeners(output, 8765)
	want := []int{4242, 4343, 4444}
	if len(got) != len(want) {
		t.Fatalf("parseNetstatListeners = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseNetstatListeners = %v, want %v", got, want)
		}
	}
}

func TestParseTasklistName(t *testing.T) {
	cases := map[string]string{
		"\r\n\"ori-agent.exe\",\"4242\",\"Console\",\"1\",\"12,345 K\"\r\n":  "ori-agent.exe",
		"\"My App, Inc.exe\",\"77\",\"Services\",\"0\",\"1,024 K\"":          "My App, Inc.exe",
		"INFO: No tasks are running which match the specified criteria.\r\n": "",
		"": "",
	}
	for input, want := range cases {
		if got := parseTasklistName([]byte(input)); got != want {
			t.Errorf("parseTasklistName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsOriProcessName(t *testing.T) {
	cases := map[string]bool{
		"ori-agent":          true,
		"ORI-AGENT":          true,
		"ori-agent.exe":      true,
		"/usr/bin/ori-agent": true,
		"ori-menubar":        true,
		"ori-menubar.exe":    true,
		"":                   false,
		"main":               false,
		"other-app":          false,
	}

	for input, expected := range cases {
		if got := IsOriProcessName(input); got != expected {
			t.Errorf("IsOriProcessName(%q) = %v, want %v", input, got, expected)
		}
	}
}

func TestFormatProcessSummary(t *testing.T) {
	processes := []ProcessInfo{{PID: 123, Name: "ori-agent"}, {PID: 0, Name: ""}}
	summary := FormatProcessSummary(processes)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if summary == "unknown process" {
		t.Fatalf("unexpected summary %q", summary)
	}
}
