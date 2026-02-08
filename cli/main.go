package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/stewartpark/private-llm/cli/infra"
	"github.com/stewartpark/private-llm/cli/tui"
)

// tuiProg is the global TUI program, set during runServe for proxy access.
var tuiProg *tui.Program

func isAddrInUse(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return errors.Is(opErr.Err, syscall.EADDRINUSE)
	}
	return false
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: private-llm [flags]
       private-llm <command> [flags]

Flags:
  -help            Display this message
  -config string   Path to agent.json (default ~/.config/private-llm/agent.json)
  -port int        Listen port (default 11434)
  -allow-all       Allow all IPs in firewall instead of just yours

Commands:
  up               Provision or reconcile infrastructure + generate certs
  down             Destroy all infrastructure
  rotate-mtls-ca   Force-rotate the CA and all certificates (use if CA is compromised)

Run 'private-llm <command> --help' for command-specific flags.
`)
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "-help") {
		usage()
		os.Exit(0)
	}

	// First non-flag arg is the subcommand; default to "" (serve)
	cmd := ""
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	switch cmd {
	case "":
		fs := flag.NewFlagSet("private-llm", flag.ExitOnError)
		port := fs.Int("port", 11434, "Listen port")
		configPath := fs.String("config", "", "Path to agent.json")
		allowAll := fs.Bool("allow-all", false, "Allow all IPs in firewall")
		fs.Usage = usage
		fs.Parse(args)
		if err := loadConfig(*configPath); err != nil {
			log.Fatalf("config error: %v", err)
		}
		runServe(ctx, cancel, *port, *allowAll)

	case "up":
		runUp(ctx, args)

	case "down", "rotate-mtls-ca":
		fs := flag.NewFlagSet("private-llm "+cmd, flag.ExitOnError)
		configPath := fs.String("config", "", "Path to agent.json")
		fs.Parse(args)
		if err := loadConfig(*configPath); err != nil {
			log.Fatalf("config error: %v", err)
		}
		switch cmd {
		case "down":
			runDown(ctx)
		case "rotate-mtls-ca":
			runRotateCA(ctx)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

// ── serve ────────────────────────────────────────────────────────

func runServe(ctx context.Context, cancel context.CancelFunc, port int, allowAllIPs bool) {
	firewallAllowAll = allowAllIPs

	// Create fullscreen TUI
	tuiProg = tui.NewProgram()

	// Start TUI in a goroutine — Run() blocks and must be running
	// before any Send() calls (bubbletea's msg channel is nil until Run starts).
	tuiDone := make(chan error, 1)
	go func() {
		tuiDone <- tuiProg.Start()
	}()

	// Wait for bubbletea to initialize its message channel
	tuiProg.WaitReady()

	// NOW safe to redirect logs and send messages
	logWriter := tuiProg.LogWriter()
	log.SetOutput(logWriter)
	log.SetFlags(log.Ltime)
	defer func() {
		logWriter.Close()
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	}()

	log.Printf("[agent] project=%s zone=%s vm=%s", cfg.ProjectID, cfg.Zone, cfg.VMName)

	// Send static config for dashboard display
	tuiProg.SetConfig(tui.ConfigMsg{
		Network:       cfg.Network,
		ListenAddr:    fmt.Sprintf("127.0.0.1:%d", port),
		Provider:      "GCP",
		MachineType:   cfg.MachineType,
		Zone:          cfg.Zone,
		ModelName:     cfg.DefaultModel,
		ContextLength: cfg.ContextLength,
	})

	// Create HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/", proxyHandler)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	server := &http.Server{Addr: addr, Handler: mux}

	// Start server
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			if isAddrInUse(err) {
				tuiProg.Done(fmt.Errorf("port %d already in use — stop Ollama first", port))
				return
			}
			tuiProg.Done(fmt.Errorf("server error: %v", err))
		}
	}()

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Polling
	pollCtx, pollCancel := context.WithCancel(ctx)

	shutdown := sync.OnceFunc(func() {
		pollCancel()
		removeFirewall(ctx)
		_ = server.Close()
		cancel()
	})

	go func() {
		<-sigCh
		shutdown()
		tuiProg.Quit()
	}()

	// Start status polling
	go pollLoop(pollCtx)

	// Handle TUI-triggered actions (r/R/S shortcuts)
	go handleActions(ctx)

	// Skip boot animation — go straight to dashboard
	tuiProg.SetView(tui.ViewDashboard)

	// Wait for TUI to exit
	if err := <-tuiDone; err != nil {
		fmt.Fprintf(os.Stderr, "[tui] error: %v\n", err)
	}

	// After alt screen clears, print any error to stderr so user can see it
	if exitErr := tuiProg.ExitError(); exitErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", exitErr)
	}

	shutdown()
}

// pollLoop sends status updates to the TUI every 5 seconds.
func pollLoop(ctx context.Context) {
	// Immediate first poll
	sendStatus(ctx)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendStatus(ctx)
		}
	}
}

// sendStatus gathers VM/cert/firewall status and sends it to the TUI.
func sendStatus(ctx context.Context) {
	if tuiProg == nil {
		return
	}

	var u tui.StatusUpdate

	// VM status
	status, err := getVMStatus(ctx)
	if err != nil {
		u.VMStatus = "NOT FOUND"
		log.Printf("[poll] VM status check: %v", err)
	} else {
		if status != "RUNNING" {
			ClearProxyReady()
		}
		if status == "RUNNING" && !IsProxyReady() {
			// VM is running but proxy hasn't connected yet — probe Ollama
			// to distinguish BOOTING from already-ready (e.g. VM was already
			// running when the TUI started).
			ip := getExternalIP(nil)
			if ip != "" && probeOllama(ctx, ip) {
				proxyReady.Store(true)
				u.VMStatus = "RUNNING"
			} else {
				u.VMStatus = "BOOTING"
			}
		} else {
			u.VMStatus = status
		}
	}

	// External IP
	u.ExternalIP = getExternalIP(nil)

	// Firewall
	u.Firewall = IsFirewallActive()
	u.SourceIP = GetCachedPublicIP()

	// Cert/token age (from local disk — use NotBefore as creation time)
	certDir := CertsDir()
	if data, err := os.ReadFile(filepath.Join(certDir, "client.crt")); err == nil {
		if block, _ := pem.Decode(data); block != nil {
			if cert, parseErr := x509.ParseCertificate(block.Bytes); parseErr == nil {
				u.CertCreated = cert.NotBefore
			}
		}
	}

	// Token was created at same time as cert
	u.TokenCreated = u.CertCreated

	// Idle time = time since last proxied request
	if lastReq := GetLastRequestTime(); !lastReq.IsZero() {
		u.IdleTime = time.Since(lastReq)
	}
	u.IdleTimeout = time.Duration(cfg.IdleTimeout) * time.Second

	// Token counts (atomic, updated in real time by proxy)
	u.InputTokens, u.OutputTokens = GetTokenCounts()

	tuiProg.SendStatus(u)
}

// ── up ───────────────────────────────────────────────────────────

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

	// ── Phase 1: Config + interactive prompts (normal terminal) ──

	existed := loadConfigFile(*pConfigPath)

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

	if cfg.ProjectID == "" {
		if p := inferProjectID(); p != "" {
			cfg.ProjectID = p
		}
	}

	if !existed {
		fmt.Println("\nSetting up Private LLM...")
		cfg.ProjectID = promptString("GCP Project ID", cfg.ProjectID)
		cfg.Zone = promptString("Zone", orDefault(cfg.Zone, "us-central1-a"))
		fmt.Println()
	}

	applyDefaults()

	if cfg.ProjectID == "" {
		log.Fatalf("project_id is required.\nUse --project-id or run: gcloud config set project <PROJECT_ID>")
	}

	if err := saveConfig(*pConfigPath); err != nil {
		log.Fatalf("failed to save config: %v", err)
	}

	// ── Phase 2: Fullscreen TUI ──

	opProg := tui.NewOperationProgram(tui.OpKindUp)

	tuiDone := make(chan error, 1)
	go func() { tuiDone <- opProg.Start() }()
	opProg.WaitReady()

	logWriter := opProg.LogWriter()
	log.SetOutput(logWriter)
	log.SetFlags(log.Ltime)
	defer func() {
		logWriter.Close()
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	}()

	log.Printf("[up] config saved to %s", configPathOrDefault(*pConfigPath))

	// Background goroutine: preview → confirm → up → certs → done
	go func() {
		// Preview
		opProg.SetPhase(tui.OpPhasePreview)
		opProg.SetStep("Previewing infrastructure changes...")
		result, err := infra.Preview(ctx, newInfraConfig(), StateDir(), logWriter)
		if err != nil {
			opProg.Done(fmt.Errorf("preview failed: %w", err))
			return
		}

		// Check if there are actual changes
		summary := make(map[string]int)
		for k, v := range result.ChangeSummary {
			summary[string(k)] = v
		}
		hasChanges := summary["create"] > 0 || summary["update"] > 0 || summary["delete"] > 0

		if hasChanges {
			// Confirm
			opProg.SetSummary(summary)
			opProg.SetPhase(tui.OpPhaseConfirm)
			if !opProg.WaitConfirm(ctx) {
				return // user cancelled — model handles done/quit
			}
		}

		// Apply
		opProg.SetPhase(tui.OpPhaseApply)
		opProg.SetStep("Provisioning infrastructure...")
		if err := infra.Up(ctx, newInfraConfig(), StateDir(), logWriter); err != nil {
			opProg.Done(fmt.Errorf("up failed: %w", err))
			return
		}

		// Certs
		opProg.SetPhase(tui.OpPhaseCerts)
		opProg.SetStep("Checking certificates...")
		smClient, err := secretmanager.NewClient(ctx)
		if err != nil {
			opProg.Done(fmt.Errorf("secret manager: %w", err))
			return
		}
		hasVersions, err := secretHasVersions(ctx, smClient, "private-llm-server-cert")
		smClient.Close()
		if err != nil {
			opProg.Done(fmt.Errorf("check secrets: %w", err))
			return
		}

		if hasVersions {
			log.Printf("[up] secrets already exist, skipping cert generation")
		} else {
			opProg.SetStep("Generating certificates...")
			if err := rotateCerts(ctx); err != nil {
				opProg.Done(fmt.Errorf("cert generation: %w", err))
				return
			}
			log.Printf("[up] certs saved to: %s", CertsDir())
		}

		log.Printf("[up] infrastructure provisioned successfully")
		log.Printf("[up] run 'private-llm' to start the proxy")
		opProg.Done(nil)
	}()

	if err := <-tuiDone; err != nil {
		fmt.Fprintf(os.Stderr, "[tui] error: %v\n", err)
	}
	if exitErr := opProg.ExitError(); exitErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", exitErr)
		os.Exit(1)
	}
}

// ── down ─────────────────────────────────────────────────────────

func runDown(ctx context.Context) {
	opProg := tui.NewOperationProgram(tui.OpKindDown)

	tuiDone := make(chan error, 1)
	go func() { tuiDone <- opProg.Start() }()
	opProg.WaitReady()

	logWriter := opProg.LogWriter()
	log.SetOutput(logWriter)
	log.SetFlags(log.Ltime)
	defer func() {
		logWriter.Close()
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	}()

	go func() {
		// Confirm
		opProg.SetPhase(tui.OpPhaseConfirm)
		if !opProg.WaitConfirm(ctx) {
			return
		}

		// Destroy
		opProg.SetPhase(tui.OpPhaseDestroy)
		opProg.SetStep("Destroying infrastructure...")
		if err := infra.Down(ctx, newInfraConfig(), StateDir(), logWriter); err != nil {
			opProg.Done(fmt.Errorf("down failed: %w", err))
			return
		}

		log.Printf("[down] infrastructure destroyed")
		opProg.Done(nil)
	}()

	if err := <-tuiDone; err != nil {
		fmt.Fprintf(os.Stderr, "[tui] error: %v\n", err)
	}
	if exitErr := opProg.ExitError(); exitErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", exitErr)
		os.Exit(1)
	}
}

// ── rotate-mtls-ca ───────────────────────────────────────────────

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

// ── TUI action handler ───────────────────────────────────────────

func handleActions(ctx context.Context) {
	for action := range tuiProg.Actions() {
		switch action {
		case tui.ActionRestartVM:
			tuiProg.Send(tui.ActionStartMsg{Label: "Restarting VM..."})
			err := restartVMWithRotation(ctx)
			sendStatus(ctx)
			tuiProg.Send(tui.ActionDoneMsg{Err: err})

		case tui.ActionResetVM:
			tuiProg.Send(tui.ActionStartMsg{Label: "Resetting VM..."})
			err := resetVMFull(ctx)
			sendStatus(ctx)
			tuiProg.Send(tui.ActionDoneMsg{Err: err})

		case tui.ActionStopVM:
			tuiProg.Send(tui.ActionStartMsg{Label: "Stopping VM..."})
			err := stopVMIfRunning(ctx)
			sendStatus(ctx)
			tuiProg.Send(tui.ActionDoneMsg{Err: err})

		case tui.ActionStartVM:
			tuiProg.Send(tui.ActionStartMsg{Label: "Starting VM..."})
			err := startVMIfStopped(ctx)
			sendStatus(ctx)
			tuiProg.Send(tui.ActionDoneMsg{Err: err})
		}
	}
}

// stopVMIfRunning stops the VM if it's running, no-op if already stopped.
// Holds setupMu to prevent races with proxy requests.
func stopVMIfRunning(ctx context.Context) error {
	setupMu.Lock()
	defer setupMu.Unlock()
	if err := stopVM(ctx); err != nil {
		return err
	}
	resetProxyState()
	removeFirewall(ctx)
	return nil
}

// startVMIfStopped opens the firewall, starts the VM if stopped, and waits
// for Ollama. No-op if already running. Holds setupMu.
func startVMIfStopped(ctx context.Context) error {
	setupMu.Lock()
	defer setupMu.Unlock()
	if err := ensureFirewallOpen(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	if _, err := ensureVMRunning(ctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	resetProxyState()
	return nil
}

// restartVMWithRotation stops the VM (if running), rotates all secrets, and
// starts it with fresh certs. Holds setupMu.
func restartVMWithRotation(ctx context.Context) error {
	setupMu.Lock()
	defer setupMu.Unlock()
	if err := stopVM(ctx); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	if err := rotateCerts(ctx); err != nil {
		return fmt.Errorf("rotate certs: %w", err)
	}
	if err := ensureFirewallOpen(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	if _, err := ensureVMRunning(ctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	resetProxyState()
	return nil
}

// resetVMFull deletes the VM, rotates secrets, and re-provisions from scratch.
// Holds setupMu.
func resetVMFull(ctx context.Context) error {
	setupMu.Lock()
	defer setupMu.Unlock()
	if err := deleteVM(ctx); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := rotateCerts(ctx); err != nil {
		return fmt.Errorf("rotate certs: %w", err)
	}
	var w io.Writer = os.Stdout
	if tuiProg != nil {
		w = tuiProg.LogWriter()
	}
	if err := infra.Up(ctx, newInfraConfig(), StateDir(), w); err != nil {
		return fmt.Errorf("recreate: %w", err)
	}
	resetProxyState()
	return nil
}

// ── helpers ──────────────────────────────────────────────────────

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
