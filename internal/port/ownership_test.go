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
	os.Stdout.WriteString("PORT " + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port) + "\n")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestBindProvesFreeOnlyWhereAnotherProcessBlocksTheBind(t *testing.T) {
	if !BindProvesFree() {
		t.Skip("this platform keeps the owner lookup ahead of the bind probe")
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
	if IsPortAvailable(port) {
		t.Fatalf("bind on :%d succeeded beside another process's loopback listener; BindProvesFree must be false here", port)
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
