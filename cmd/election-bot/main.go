package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/Diaszano/election-bot/internal/platform/config"
	"github.com/Diaszano/election-bot/internal/platform/lifecycle"
	"github.com/Diaszano/election-bot/internal/platform/observability"
	"github.com/mdp/qrterminal/v3"
	"go.opentelemetry.io/otel"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if handled, err := dispatchWhatsApp(ctx, os.Args[1:], runWhatsAppPair, runWhatsAppLogout); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	simulate := flag.Bool("simulate", false, "run deterministic offline recovery check")
	health := flag.Bool("healthcheck", false, "probe local liveness")
	flag.Parse()
	if *health {
		if e := healthcheck(); e != nil {
			os.Exit(1)
		}
		return
	}
	var e error
	if *simulate {
		dir, x := os.MkdirTemp("", "election-bot-simulation-")
		if x != nil {
			e = x
		} else {
			defer os.RemoveAll(dir)
			c, x := config.LoadForMode("simulation")
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

func dispatchWhatsApp(ctx context.Context, args []string, pair, logout func(context.Context) error) (bool, error) {
	if len(args) == 0 || args[0] != "whatsapp" {
		return false, nil
	}
	if len(args) != 2 {
		return true, errors.New("usage: election-bot whatsapp pair|logout")
	}
	switch args[1] {
	case "pair":
		return true, pair(ctx)
	case "logout":
		return true, logout(ctx)
	default:
		return true, errors.New("usage: election-bot whatsapp pair|logout")
	}
}

func runWhatsAppPair(ctx context.Context) error {
	c, err := config.LoadForMode("pair")
	if err != nil {
		return err
	}
	render := func(out io.Writer, code string) error {
		qrterminal.GenerateHalfBlock(code, qrterminal.M, out)
		return nil
	}
	if err = lifecycle.PairWhatsApp(ctx, c, os.Stdout, isTerminal(os.Stdout), render); err != nil {
		return err
	}
	fmt.Println("WhatsApp pairing completed and session saved.")
	return nil
}
func runWhatsAppLogout(ctx context.Context) error {
	c, err := config.LoadForMode("logout")
	if err != nil {
		return err
	}
	if err = lifecycle.LogoutWhatsApp(ctx, c); err != nil {
		return err
	}
	fmt.Println("WhatsApp session logged out.")
	return nil
}
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
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
