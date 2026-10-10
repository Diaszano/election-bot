package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/Diaszano/election-bot/internal/platform/config"
	"github.com/Diaszano/election-bot/internal/platform/lifecycle"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	simulate := flag.Bool("simulate", false, "run deterministic offline recovery check")
	health := flag.Bool("healthcheck", false, "probe local liveness")
	flag.Parse()
	if *health {
		client := &http.Client{Timeout: 2 * time.Second}
		r, e := client.Get("http://127.0.0.1:8080/health/live")
		if e != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	var e error
	if *simulate {
		dir, x := os.MkdirTemp("", "election-bot-simulation-")
		if x != nil {
			e = x
		} else {
			defer os.RemoveAll(dir)
			e = lifecycle.Simulate(ctx, filepath.Join(dir, "elections.db"))
		}
		if e == nil {
			fmt.Println("election-bot simulation: PASS")
		}
	} else {
		c, x := config.Load()
		if x != nil {
			e = x
		} else {
			e = lifecycle.Run(ctx, c)
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
