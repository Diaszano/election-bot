package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/Diaszano/election-bot/internal/platform/config"
	"github.com/Diaszano/election-bot/internal/platform/lifecycle"
	"github.com/Diaszano/election-bot/internal/platform/observability"
	"go.opentelemetry.io/otel"
	"net"
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
		if e := healthcheck(); e != nil {
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
			c, x := config.Load()
			if x != nil {
				e = x
			} else {
				runtime, x := observability.Setup(ctx, c, "dev")
				if x != nil {
					e = x
				} else {
					operation, span := otel.Tracer("election-bot").Start(ctx, "simulation.run")
					runtime.Logger.InfoContext(operation, "election-bot simulation started")
					e = lifecycle.Simulate(operation, filepath.Join(dir, "elections.db"))
					span.End()
					shutdown, stop := context.WithTimeout(context.Background(), c.ShutdownTimeout)
					if x = runtime.Shutdown(shutdown); x != nil {
						runtime.Logger.Warn("telemetry shutdown incomplete")
					}
					stop()
				}
			}
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

func healthcheck() error {
	address := os.Getenv("HTTP_LISTEN_ADDRESS")
	if address == "" {
		address = ":8080"
	}
	_, port, e := net.SplitHostPort(address)
	if e != nil {
		return errors.New("invalid health listen address")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	r, e := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/health/live")
	if e != nil {
		return errors.New("local health probe failed")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return errors.New("local health unavailable")
	}
	return nil
}
