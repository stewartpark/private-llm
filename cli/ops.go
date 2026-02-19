package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"github.com/stewartpark/private-llm/cli/infra"
	"github.com/stewartpark/private-llm/cli/tui"
)

// InfraOps serializes all infrastructure state transitions through a single
// goroutine. The proxy never mutates infra state — it reads cached state,
// forwards requests, and signals the ops loop on failure.
type InfraOps struct {
	mu         sync.Mutex
	recoveryCh chan struct{} // buffered 1, deduplicates concurrent signals
}

var ops = InfraOps{
	recoveryCh: make(chan struct{}, 1),
}

// Run is the ops event loop. It waits for boot/recovery signals (from
// the proxy on first request) or TUI actions. Must be called from a
// single goroutine.
func (o *InfraOps) Run(ctx context.Context, actions <-chan tui.Action) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-o.recoveryCh:
			o.mu.Lock()
			o.doSetup(ctx)
			o.mu.Unlock()
		case action := <-actions:
			o.mu.Lock()
			o.handleAction(ctx, action)
			o.mu.Unlock()
		}
	}
}

// EnsureSetup signals the ops loop to run doSetup if the gate is closed
// (VM not yet booted). No-op if the gate is already open. Called by the
// proxy on each incoming request so the VM boots lazily on first use.
func (o *InfraOps) EnsureSetup() {
	readyMu.Lock()
	ch := readyCh
	readyMu.Unlock()
	select {
	case <-ch: // gate already open — VM is ready
		return
	default:
	}
	select {
	case o.recoveryCh <- struct{}{}:
	default: // already signaled
	}
}

// RequestRecovery closes the gate immediately and signals the ops loop.
// Safe to call from multiple goroutines — deduplicates via buffered channel.
func (o *InfraOps) RequestRecovery() {
	closeGate()
	select {
	case o.recoveryCh <- struct{}{}:
	default: // already signaled, ops loop will handle it
	}
}

// RemoveFirewall removes the dynamic firewall rule. Called during shutdown.
func (o *InfraOps) RemoveFirewall(ctx context.Context) {
	o.mu.Lock()
	defer o.mu.Unlock()
	removeFirewall(ctx)
}

// handleAction dispatches TUI actions under the ops lock.
func (o *InfraOps) handleAction(ctx context.Context, action tui.Action) {
	switch action {
	case tui.ActionRestartVM:
		tuiProg.Send(tui.ActionStartMsg{Label: "Restarting VM..."})
		err := o.doRestartVM(ctx)
		if err == nil {
			o.doSetup(ctx)
		}
		sendStatus(ctx)
		tuiProg.Send(tui.ActionDoneMsg{Err: err})

	case tui.ActionResetVM:
		tuiProg.Send(tui.ActionStartMsg{Label: "Resetting VM..."})
		err := o.doResetVM(ctx)
		if err == nil {
			o.doSetup(ctx)
		}
		sendStatus(ctx)
		tuiProg.Send(tui.ActionDoneMsg{Err: err})

	case tui.ActionStopVM:
		tuiProg.Send(tui.ActionStartMsg{Label: "Stopping VM..."})
		err := o.doStopVM(ctx)
		sendStatus(ctx)
		tuiProg.Send(tui.ActionDoneMsg{Err: err})

	case tui.ActionStartVM:
		tuiProg.Send(tui.ActionStartMsg{Label: "Starting VM..."})
		err := o.doStartVM(ctx)
		if err == nil {
			o.doSetup(ctx)
		}
		sendStatus(ctx)
		tuiProg.Send(tui.ActionDoneMsg{Err: err})
	}
}

// doSetup verifies the VM is running; if stopped it re-runs the full startup:
// rotate certs, start VM, wait for Ollama, open gate.
func (o *InfraOps) doSetup(ctx context.Context) {
	// If we have a cached IP, verify the VM is still running
	if vmIP != "" {
		stopped, err := isVMStopped(ctx)
		if err != nil {
			log.Printf("[setup] VM status check failed: %v, using cached IP", err)
			openGate()
			return
		}
		if !stopped {
			openGate()
			return
		}
		log.Printf("[setup] VM is stopped, restarting full setup...")
		resetProxyState()
	}

	if err := ensureFirewallOpen(ctx); err != nil {
		log.Printf("[setup] firewall failed: %v", err)
		return
	}

	needsStart, err := isVMStopped(ctx)
	if err != nil {
		log.Printf("[setup] VM status check failed: %v", err)
		return
	}

	// Only rotate certs when VM is stopped — it fetches certs from SM on boot
	if needsStart && !rotatedOnce {
		log.Printf("[setup] rotating certificates (VM will boot with fresh certs)...")
		if err := rotateCerts(ctx); err != nil {
			log.Printf("[setup] cert rotation failed: %v", err)
			return
		}
		rotatedOnce = true
		sendStatus(ctx)
	}

	ip, err := ensureVMRunning(ctx)
	if err != nil {
		log.Printf("[setup] VM start failed: %v", err)
		return
	}

	vmIP = ip
	proxyReady.Store(true)
	openGate()
}

// doStopVM stops the VM, clears proxy state, and removes the firewall.
func (o *InfraOps) doStopVM(ctx context.Context) error {
	closeGate()
	if err := stopVM(ctx); err != nil {
		return err
	}
	resetProxyState()
	removeFirewall(ctx)
	return nil
}

// doStartVM opens the firewall and starts the VM if stopped.
func (o *InfraOps) doStartVM(ctx context.Context) error {
	if err := ensureFirewallOpen(ctx); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	if _, err := ensureVMRunning(ctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	resetProxyState()
	return nil
}

// doRestartVM stops the VM, rotates certs, opens firewall, and starts VM.
func (o *InfraOps) doRestartVM(ctx context.Context) error {
	closeGate()
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

// doResetVM deletes the VM, rotates certs, and re-provisions from scratch.
func (o *InfraOps) doResetVM(ctx context.Context) error {
	closeGate()
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
	stateDir, _ := StateDir()
	if err := infra.Up(ctx, newInfraConfig(), stateDir, w); err != nil {
		return fmt.Errorf("recreate: %w", err)
	}
	resetProxyState()
	return nil
}
