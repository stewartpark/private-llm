package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func isAddrInUse(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return errors.Is(opErr.Err, syscall.EADDRINUSE)
	}
	return false
}

// Config holds agent configuration loaded from agent.json.
type Config struct {
	ProjectID string `json:"project_id"`
	Zone      string `json:"zone"`
	VMName    string `json:"vm_name"`
	Network   string `json:"network"`
}

var cfg Config

func main() {
	port := flag.Int("port", 11434, "Listen port")
	configPath := flag.String("config", "", "Path to agent.json (default: ~/.config/private-llm/agent.json)")
	flag.Parse()

	// Load config
	cfgFile := *configPath
	if cfgFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("failed to get home dir: %v", err)
		}
		cfgFile = filepath.Join(home, ".config", "private-llm", "agent.json")
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		log.Fatalf("failed to read config %s: %v", cfgFile, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}
	if cfg.ProjectID == "" || cfg.Zone == "" || cfg.VMName == "" || cfg.Network == "" {
		log.Fatalf("config must include project_id, zone, vm_name, network")
	}

	log.Printf("[agent] project=%s zone=%s vm=%s network=%s", cfg.ProjectID, cfg.Zone, cfg.VMName, cfg.Network)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	startHeartbeat(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/", proxyHandler)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	server := &http.Server{Addr: addr, Handler: mux}

	go func() {
		sig := <-sigCh
		log.Printf("[agent] received %v, shutting down...", sig)

		// Delete firewall rule
		removeFirewall(ctx)

		_ = server.Close()
		cancel()
	}()

	log.Printf("[agent] listening on %s", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		if isAddrInUse(err) {
			log.Fatalf("port %d is already in use — stop Ollama first, since this agent acts as Ollama.", *port)
		}
		log.Fatalf("server error: %v", err)
	}
	log.Printf("[agent] shutdown complete")
}
