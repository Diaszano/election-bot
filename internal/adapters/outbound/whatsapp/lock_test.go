package whatsapp

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionLockHelperProcess(t *testing.T) {
	if os.Getenv("ELECTION_BOT_LOCK_HELPER") == "" {
		return
	}
	path := os.Getenv("ELECTION_BOT_LOCK_PATH")
	lock, err := AcquireSessionLock(path)
	if os.Getenv("ELECTION_BOT_LOCK_EXPECT") == "busy" {
		if err == nil {
			lock.Close()
			os.Exit(2)
		}
		os.Exit(0)
	}
	if err != nil {
		os.Exit(3)
	}
	if os.Getenv("ELECTION_BOT_LOCK_HOLD") == "1" {
		fmt.Println("LOCKED")
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	lock.Close()
	os.Exit(0)
}

func TestSessionLockAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whatsapp-session.db")
	lock, err := AcquireSessionLock(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionLockHelperProcess$")
	cmd.Env = append(os.Environ(), "ELECTION_BOT_LOCK_HELPER=1", "ELECTION_BOT_LOCK_PATH="+path, "ELECTION_BOT_LOCK_EXPECT=busy")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second process unexpectedly acquired lock: %s: %v", output, err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(os.Args[0], "-test.run=^TestSessionLockHelperProcess$")
	cmd.Env = append(os.Environ(), "ELECTION_BOT_LOCK_HELPER=1", "ELECTION_BOT_LOCK_PATH="+path, "ELECTION_BOT_LOCK_HOLD=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "LOCKED" {
		cmd.Process.Kill()
		t.Fatalf("helper did not acquire lock: %q %v", line, err)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	lock, err = AcquireSessionLock(path)
	if err != nil {
		t.Fatalf("lock not released after process death: %v", err)
	}
	lock.Close()
}

func TestSessionLockPathAliases(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	first, err := AcquireSessionLock(filepath.Join(real, "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := AcquireSessionLock(filepath.Join(alias, "session.db")); err == nil {
		second.Close()
		t.Fatal("canonical path alias bypassed session lock")
	}
}
