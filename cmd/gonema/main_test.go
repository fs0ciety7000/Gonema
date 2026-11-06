package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunVersionAndFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	getenv := func(string) string { return "" }
	if code := run(context.Background(), []string{"version"}, &out, &errOut, getenv); code != 0 || out.String() != "gonema dev\n" {
		t.Errorf("version = %d %q", code, out.String())
	}
	if code := run(context.Background(), []string{"serve", "-bind", "nope"}, &out, &errOut, getenv); code != 2 ||
		!strings.Contains(errOut.String(), "bind address") {
		t.Errorf("bad flag = %d %q", code, errOut.String())
	}
	errOut.Reset()
	if code := run(context.Background(), []string{"-h"}, &out, &errOut, getenv); code != 0 || !strings.Contains(errOut.String(), "GONEMA_DATA_DIR") {
		t.Errorf("help = %d %q", code, errOut.String())
	}
}

// coldStartBudget is the performance budget from docs/architecture.md: the
// server answers /health within one second of being launched.
const coldStartBudget = time.Second

// TestColdStart builds the binary, starts it on an empty data directory (first
// run: key creation, migrations, profile seeding) and measures the time until
// /health answers 200. It then checks the graceful shutdown on SIGTERM.
func TestColdStart(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	if runtime.GOOS == "windows" {
		t.Skip("uses SIGTERM")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	bin := filepath.Join(t.TempDir(), "gonema")
	build := exec.Command(goBin, "build", "-trimpath", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	dataDir := t.TempDir()
	cmd := exec.Command(bin, "serve")
	cmd.Env = append(os.Environ(), "GONEMA_DATA_DIR="+dataDir, "GONEMA_BIND=127.0.0.1:0", "GONEMA_LOG_LEVEL=info", "GONEMA_MASTER_KEY=")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	exited := false
	t.Cleanup(func() {
		if !exited {
			_ = cmd.Process.Kill()
			if err := <-done; err != nil {
				t.Logf("killed server: %v", err)
			}
		}
	})

	addr := make(chan string, 1)
	logs := make(chan string, 1024)
	re := regexp.MustCompile(`address=(http://[0-9.:]+)/`)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			select {
			case logs <- line:
			default:
			}
			if m := re.FindStringSubmatch(line); m != nil {
				addr <- m[1]
			}
		}
		done <- cmd.Wait()
	}()

	var base string
	select {
	case base = <-addr:
	case <-time.After(10 * time.Second):
		t.Fatalf("no ready log line; logs:\n%s", drain(logs))
	}
	client := &http.Client{Timeout: time.Second}
	var ready time.Duration
	for {
		resp, err := client.Get(base + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = time.Since(start)
				break
			}
		}
		if time.Since(start) > 10*time.Second {
			t.Fatalf("/health never answered 200 (last error %v)", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Logf("cold start: /health 200 after %s (budget %s)", ready.Round(time.Millisecond), coldStartBudget)
	if ready > coldStartBudget {
		t.Errorf("cold start took %s, budget %s", ready, coldStartBudget)
	}
	// The web interface (or its placeholder) is served at the root.
	if resp, err := client.Get(base + "/movies"); err != nil {
		t.Errorf("GET /movies: %v", err)
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") ||
			!strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("GET /movies = %d %v", resp.StatusCode, resp.Header)
		}
	}
	// The API needs a caller; the auth status is public and asks for setup.
	if resp, err := client.Get(base + "/api/v1/system/status"); err != nil {
		t.Errorf("GET /api/v1/system/status: %v", err)
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("unauthenticated API = %d", resp.StatusCode)
		}
	}
	if resp, err := client.Get(base + "/api/v1/auth/status"); err != nil {
		t.Errorf("GET /api/v1/auth/status: %v", err)
	} else {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"setupRequired":true`) {
			t.Errorf("auth status = %d %s", resp.StatusCode, b)
		}
	}
	for _, f := range []string{"gonema.db", "gonema.key"} {
		if _, err := os.Stat(filepath.Join(dataDir, f)); err != nil {
			t.Errorf("%s not created: %v", f, err)
		}
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		exited = true
		if err != nil {
			t.Errorf("exit after SIGTERM: %v\n%s", err, drain(logs))
		}
	case <-time.After(20 * time.Second):
		t.Fatal("no exit within 20 s of SIGTERM")
	}
	if l := drain(logs); !strings.Contains(l, "gonema stopped") {
		t.Errorf("no shutdown log line:\n%s", l)
	}
}

func drain(c chan string) string {
	var b strings.Builder
	for {
		select {
		case l := <-c:
			b.WriteString(l)
			b.WriteByte('\n')
		default:
			return b.String()
		}
	}
}
