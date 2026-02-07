package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/stewartpark/private-llm/cli/infra"
)

func isAddrInUse(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return errors.Is(opErr.Err, syscall.EADDRINUSE)
	}
	return false
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: private-llm [command] [flags]

Commands:
  serve            Start the proxy server (default)
  up               Provision or reconcile infrastructure + generate certs
  down             Destroy all infrastructure
  preview          Show what infrastructure changes would be made
  restart-vm       Stop the VM, rotate certs, and start it again
  reset-vm         Delete the VM and recreate it from scratch
  rotate-mtls-ca   Force-rotate the CA and all certificates (use if CA is compromised)

Run 'private-llm <command> --help' for command-specific flags.
`)
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "-help") {
		usage()
		os.Exit(0)
	}

	// First non-flag arg is the subcommand; default to serve
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	switch cmd {
	case "serve":
		fs := flag.NewFlagSet("private-llm serve", flag.ExitOnError)
		port := fs.Int("port", 11434, "Listen port")
		configPath := fs.String("config", "", "Path to agent.json")
		allowAll := fs.Bool("allow-all", false, "Allow all IPs in firewall")
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: private-llm [serve] [flags]\n\nStart the local Ollama-compatible proxy.\n\nFlags:\n")
			fs.PrintDefaults()
		}
		fs.Parse(args)
		if err := loadConfig(*configPath); err != nil {
			log.Fatalf("config error: %v", err)
		}
		runServe(ctx, cancel, *port, *allowAll)

	case "up":
		runUp(ctx, args)

	case "down", "preview", "restart-vm", "reset-vm", "rotate-mtls-ca":
		fs := flag.NewFlagSet("private-llm "+cmd, flag.ExitOnError)
		configPath := fs.String("config", "", "Path to agent.json")
		fs.Parse(args)
		if err := loadConfig(*configPath); err != nil {
			log.Fatalf("config error: %v", err)
		}
		switch cmd {
		case "down":
			runDown(ctx)
		case "preview":
			runPreview(ctx)
		case "restart-vm":
			runRestartVM(ctx)
		case "reset-vm":
			runResetVM(ctx)
		case "rotate-mtls-ca":
			runRotateCA(ctx)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func runServe(ctx context.Context, cancel context.CancelFunc, port int, allowAllIPs bool) {
	firewallAllowAll = allowAllIPs
	log.Printf("[agent] project=%s zone=%s vm=%s network=%s", cfg.ProjectID, cfg.Zone, cfg.VMName, cfg.Network)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	mux := http.NewServeMux()
	mux.HandleFunc("/", proxyHandler)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	server := &http.Server{Addr: addr, Handler: mux}

	go func() {
		sig := <-sigCh
		log.Printf("[agent] received %v, shutting down...", sig)

		removeFirewall(ctx)

		_ = server.Close()
		cancel()
	}()

	log.Printf("[agent] listening on %s", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		if isAddrInUse(err) {
			log.Fatalf("port %d is already in use — stop Ollama first, since this agent acts as Ollama.", port)
		}
		log.Fatalf("server error: %v", err)
	}
	log.Printf("[agent] shutdown complete")
}

func runUp(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("private-llm up", flag.ExitOnError)
	pConfigPath := fs.String("config", "", "Path to agent.json")
	pProjectID := fs.String("project-id", "", "GCP project ID (default: inferred from gcloud)")
	pZone := fs.String("zone", "", "GCP zone (default: us-central1-a)")
	pVMName := fs.String("vm-name", "", "VM instance name (default: private-llm-vm)")
	pNetwork := fs.String("network", "", "VPC network name (default: private-llm)")
	pRegion := fs.String("region", "", "GCP region (default: derived from zone)")
	pMachineType := fs.String("machine-type", "", "VM machine type (default: g4-standard-48)")
	pDefaultModel := fs.String("default-model", "", "Ollama model (default: qwen3-coder-next:q8_0)")
	pContextLength := fs.Int("context-length", 0, "Context window size (default: 262144)")
	pIdleTimeout := fs.Int("idle-timeout", 0, "Idle shutdown seconds (default: 300)")
	pSubnetCIDR := fs.String("subnet-cidr", "", "Subnet CIDR (default: 10.10.0.0/24)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: private-llm up [flags]\n\nProvision or reconcile cloud infrastructure and generate mTLS certificates.\nOn first run, prompts for required values interactively.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	// Load existing config as baseline (not required to exist)
	existed := loadConfigFile(*pConfigPath)

	// CLI flags override config file
	if *pProjectID != "" {
		cfg.ProjectID = *pProjectID
	}
	if *pZone != "" {
		cfg.Zone = *pZone
	}
	if *pVMName != "" {
		cfg.VMName = *pVMName
	}
	if *pNetwork != "" {
		cfg.Network = *pNetwork
	}
	if *pRegion != "" {
		cfg.Region = *pRegion
	}
	if *pMachineType != "" {
		cfg.MachineType = *pMachineType
	}
	if *pDefaultModel != "" {
		cfg.DefaultModel = *pDefaultModel
	}
	if *pContextLength != 0 {
		cfg.ContextLength = *pContextLength
	}
	if *pIdleTimeout != 0 {
		cfg.IdleTimeout = *pIdleTimeout
	}
	if *pSubnetCIDR != "" {
		cfg.SubnetCIDR = *pSubnetCIDR
	}

	// Infer project ID from gcloud if not set
	if cfg.ProjectID == "" {
		if p := inferProjectID(); p != "" {
			cfg.ProjectID = p
		}
	}

	// Interactive prompt when no config file exists
	if !existed {
		fmt.Println("\nSetting up Private LLM...")
		cfg.ProjectID = promptString("GCP Project ID", cfg.ProjectID)
		cfg.Zone = promptString("Zone", orDefault(cfg.Zone, "us-central1-a"))
		fmt.Println()
	}

	// Apply defaults for everything else
	applyDefaults()

	if cfg.ProjectID == "" {
		log.Fatalf("project_id is required.\nUse --project-id or run: gcloud config set project <PROJECT_ID>")
	}

	// Save config for future commands
	p := configPathOrDefault(*pConfigPath)
	if err := saveConfig(*pConfigPath); err != nil {
		log.Fatalf("failed to save config: %v", err)
	}
	log.Printf("[up] config saved to %s", p)

	// Provision infrastructure
	log.Printf("[up] provisioning infrastructure...")

	if err := infra.Up(ctx, newInfraConfig(), StateDir()); err != nil {
		log.Fatalf("up failed: %v", err)
	}

	// Generate certs after infra is provisioned
	log.Printf("[up] generating certificates...")
	if err := rotateCerts(ctx); err != nil {
		log.Fatalf("cert generation failed: %v", err)
	}

	log.Printf("[up] infrastructure provisioned and certs generated")
	log.Printf("[up] certs at: %s", CertsDir())
	log.Printf("[up] run 'private-llm' to start the proxy")
}

func runDown(ctx context.Context) {
	log.Printf("[down] destroying infrastructure...")

	if err := infra.Down(ctx, newInfraConfig(), StateDir()); err != nil {
		log.Fatalf("down failed: %v", err)
	}

	log.Printf("[down] infrastructure destroyed")
}

func runRestartVM(ctx context.Context) {
	log.Printf("[restart-vm] stopping VM...")
	if err := stopVM(ctx); err != nil {
		log.Fatalf("stop failed: %v", err)
	}

	log.Printf("[restart-vm] rotating certificates...")
	if err := rotateCerts(ctx); err != nil {
		log.Fatalf("cert rotation failed: %v", err)
	}

	log.Printf("[restart-vm] ensuring firewall open...")
	if err := ensureFirewallOpen(ctx); err != nil {
		log.Fatalf("firewall setup failed: %v", err)
	}

	log.Printf("[restart-vm] starting VM...")
	ip, err := ensureVMRunning(ctx)
	if err != nil {
		log.Fatalf("start failed: %v", err)
	}

	removeFirewall(ctx)
	log.Printf("[restart-vm] VM restarted at %s", ip)
}

func runResetVM(ctx context.Context) {
	log.Printf("[reset-vm] deleting VM...")
	if err := deleteVM(ctx); err != nil {
		log.Fatalf("delete failed: %v", err)
	}

	log.Printf("[reset-vm] rotating certificates...")
	if err := rotateCerts(ctx); err != nil {
		log.Fatalf("cert rotation failed: %v", err)
	}

	log.Printf("[reset-vm] recreating VM via Pulumi...")
	if err := infra.Up(ctx, newInfraConfig(), StateDir()); err != nil {
		log.Fatalf("recreate failed: %v", err)
	}

	log.Printf("[reset-vm] VM recreated")
}

func runRotateCA(ctx context.Context) {
	certDir := CertsDir()

	for _, name := range []string{"ca.crt", "ca.key"} {
		p := filepath.Join(certDir, name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Fatalf("failed to remove %s: %v", p, err)
		}
	}

	log.Printf("[rotate-ca] deleted existing CA, regenerating entire cert chain...")
	if err := rotateCerts(ctx); err != nil {
		log.Fatalf("CA rotation failed: %v", err)
	}

	log.Printf("[rotate-ca] new CA + server cert + client cert + token generated")
	log.Printf("[rotate-ca] certs at: %s", certDir)
	log.Printf("[rotate-ca] restart the VM to pick up new server certs")
}

func runPreview(ctx context.Context) {
	log.Printf("[preview] previewing infrastructure changes...")

	if err := infra.Preview(ctx, newInfraConfig(), StateDir()); err != nil {
		log.Fatalf("preview failed: %v", err)
	}
}

func newInfraConfig() *infra.InfraConfig {
	return &infra.InfraConfig{
		ProjectID:     cfg.ProjectID,
		Region:        cfg.Region,
		Zone:          cfg.Zone,
		VMName:        cfg.VMName,
		Network:       cfg.Network,
		MachineType:   cfg.MachineType,
		DefaultModel:  cfg.DefaultModel,
		ContextLength: cfg.ContextLength,
		IdleTimeout:   cfg.IdleTimeout,
		SubnetCIDR:    cfg.SubnetCIDR,
		StartupScript: vmStartupScript,
		Caddyfile:     caddyfileContent,
	}
}

func orDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}
