package integration

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestExecutableSIGTERMAndUnavailableTelemetry(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "election-bot")
	build := exec.Command("go", "build", "-o", binary, "./cmd/election-bot")
	build.Dir = "../.."
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, output)
	}
	var addr string
	for attempt := 0; attempt < 10; attempt++ {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e == nil {
			addr = l.Addr().String()
			_ = l.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("could not find free port")
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "TELEGRAM_ENABLED=false", "OTEL_ENABLED=true", "OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:1", "HTTP_LISTEN_ADDRESS="+addr, "SQLITE_PATH="+filepath.Join(t.TempDir(), "app.db"))
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		r, e := client.Get("http://" + addr + "/health/ready")
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("not ready with unavailable telemetry; output: %s", output.String())
	}
	start := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("shutdown: %v %s", e, output.String())
		}
	case <-time.After(11 * time.Second):
		t.Fatal("shutdown deadline exceeded")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("slow shutdown")
	}
}
