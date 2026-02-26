package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/stewartpark/private-llm/cli/tui/assets"
	"golang.org/x/term"
)

// Colors matching the TUI palette.
var (
	setupCyan  = lipgloss.Color("#22d3ee")
	setupGreen = lipgloss.Color("#4ade80")
	setupGray  = lipgloss.Color("#6b7280")
	setupDim   = lipgloss.Color("#374151")
	setupRed   = lipgloss.Color("#f87171")
)

// zonesForFamily maps GPU machine family to zones where that family is available.
// Source: https://cloud.google.com/compute/docs/gpus/gpu-regions-zones (2026-02-05)
var zonesForFamily = map[string][]string{
	"g2": {
		// US
		"us-central1-a", "us-central1-b", "us-central1-c", "us-central1-f",
		"us-east1-b", "us-east1-c",
		"us-east4-a", "us-east4-c",
		"us-west1-a", "us-west1-b", "us-west1-c",
		"us-west4-a", "us-west4-c",
		// Canada
		"northamerica-northeast2-a", "northamerica-northeast2-b",
		// Europe
		"europe-west1-b", "europe-west1-c",
		"europe-west2-a", "europe-west2-b",
		"europe-west3-a", "europe-west3-b",
		"europe-west4-a", "europe-west4-b", "europe-west4-c",
		"europe-west6-b", "europe-west6-c",
		// Middle East
		"me-central2-a", "me-central2-c",
		// Asia
		"asia-east1-a", "asia-east1-b", "asia-east1-c",
		"asia-northeast1-a", "asia-northeast1-b", "asia-northeast1-c",
		"asia-northeast3-a", "asia-northeast3-b",
		"asia-south1-a", "asia-south1-b", "asia-south1-c",
		"asia-southeast1-a", "asia-southeast1-b", "asia-southeast1-c",
	},
	"g4": {
		// US
		"us-central1-b", "us-central1-f",
		"us-east1-b",
		"us-east4-c",
		"us-east5-a", "us-east5-b", "us-east5-c",
		"us-south1-a", "us-south1-b",
		"us-west1-b", "us-west1-c",
		"us-west3-a",
		"us-west4-a",
		// Europe
		"europe-north1-a",
		"europe-west1-c",
		"europe-west2-b",
		"europe-west4-a", "europe-west4-b", "europe-west4-c",
		"europe-west8-b",
		// Asia
		"asia-southeast1-b", "asia-southeast1-c",
		"asia-southeast2-b", "asia-southeast2-c",
	},
	"a2": {
		// US
		"us-central1-a", "us-central1-b", "us-central1-c", "us-central1-f",
		"us-east1-b",
		"us-west1-b",
		"us-west3-b",
		"us-west4-b",
		// Europe
		"europe-west4-a", "europe-west4-b",
		// Middle East
		"me-west1-a", "me-west1-c",
		// Asia
		"asia-northeast1-a", "asia-northeast1-c",
		"asia-southeast1-b", "asia-southeast1-c",
	},
	"a3": {
		// US
		"us-central1-a", "us-central1-b", "us-central1-c",
		"us-east1-d",
		"us-east4-a", "us-east4-b", "us-east4-c",
		"us-east5-a",
		"us-south1-b",
		"us-west1-a", "us-west1-b",
		"us-west4-a",
		// Canada
		"northamerica-northeast2-c",
		// Europe
		"europe-west1-b", "europe-west1-c",
		"europe-west4-b", "europe-west4-c",
		"europe-west9-c",
		// Australia
		"australia-southeast1-c",
		// Asia
		"asia-east1-c",
		"asia-northeast1-b",
		"asia-northeast3-a",
		"asia-south1-b", "asia-south1-c",
		"asia-southeast1-b", "asia-southeast1-c",
	},
	"a4": {
		// US
		"us-central1-a",
		"us-east1-b",
		"us-east4-b",
		"us-south1-b",
		"us-west2-c",
		"us-west3-b", "us-west3-c",
		// Europe
		"europe-west4-b",
		// Asia
		"asia-northeast1-b",
		"asia-southeast1-b",
	},
}

// machineFamily extracts the family prefix (e.g. "g2", "a2") from a machine type.
func machineFamily(mt string) string {
	if i := strings.IndexByte(mt, '-'); i > 0 {
		return mt[:i]
	}
	return mt
}

// zonesForMachineType returns the available zones for the given machine type.
func zonesForMachineType(mt string) []string {
	return zonesForFamily[machineFamily(mt)]
}

// GPU machine types: L4 (g2), RTX PRO 6000 (g4), A100 (a2), H100 (a3), B200 (a4).
var machineTypeOptions = []string{
	// G2 — NVIDIA L4 24 GB
	"g2-standard-4",  // 1x L4, 4 vCPU, 16 GB
	"g2-standard-8",  // 1x L4, 8 vCPU, 32 GB
	"g2-standard-12", // 1x L4, 12 vCPU, 48 GB
	"g2-standard-16", // 1x L4, 16 vCPU, 64 GB
	"g2-standard-24", // 2x L4, 24 vCPU, 96 GB
	"g2-standard-32", // 1x L4, 32 vCPU, 128 GB
	"g2-standard-48", // 4x L4, 48 vCPU, 192 GB
	"g2-standard-96", // 8x L4, 96 vCPU, 384 GB
	// G4 — NVIDIA RTX PRO 6000 96 GB
	"g4-standard-48",  // 1x RTX PRO 6000, 48 vCPU, 180 GB
	"g4-standard-96",  // 2x RTX PRO 6000, 96 vCPU, 360 GB
	"g4-standard-192", // 4x RTX PRO 6000, 192 vCPU, 720 GB
	"g4-standard-384", // 8x RTX PRO 6000, 384 vCPU, 1440 GB
	// A2 — NVIDIA A100 40 GB
	"a2-highgpu-1g", // 1x A100, 12 vCPU, 85 GB
	"a2-highgpu-2g", // 2x A100, 24 vCPU, 170 GB
	"a2-highgpu-4g", // 4x A100, 48 vCPU, 340 GB
	"a2-highgpu-8g", // 8x A100, 96 vCPU, 680 GB
	// A3 — NVIDIA H100 80 GB
	"a3-highgpu-1g", // 1x H100, 26 vCPU, 234 GB
	"a3-highgpu-2g", // 2x H100, 52 vCPU, 468 GB
	"a3-highgpu-4g", // 4x H100, 104 vCPU, 936 GB
	"a3-highgpu-8g", // 8x H100, 208 vCPU, 1872 GB
	// A4 — NVIDIA B200 180 GB
	"a4-highgpu-8g", // 8x B200, 224 vCPU, 3968 GB
}

const selectMaxVisible = 10

// sectionHeader prints a bold cyan label with a dim rule line.
func sectionHeader(label string) {
	styled := lipgloss.NewStyle().Bold(true).Foreground(setupCyan).Render(label)
	ruleLen := 40 - len(label) - 1
	if ruleLen < 4 {
		ruleLen = 4
	}
	rule := lipgloss.NewStyle().Foreground(setupDim).Render(strings.Repeat("\u2500", ruleLen))
	fmt.Printf("\n  \u2500\u2500 %s %s\n", styled, rule)
}

// promptSelect shows an arrow-key navigable list and returns the chosen option.
// Falls back to numbered input if the terminal doesn't support raw mode.
func promptSelect(label string, options []string, defaultIdx int) string {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return promptSelectFallback(label, options, defaultIdx)
	}

	selected := defaultIdx
	viewSize := min(selectMaxVisible, len(options))
	scrollable := len(options) > viewSize
	offset := 0

	// Ensure selected item is initially visible
	if selected >= viewSize {
		offset = selected - viewSize + 1
	}

	adjustScroll := func() {
		if selected < offset {
			offset = selected
		}
		if selected >= offset+viewSize {
			offset = selected - viewSize + 1
		}
	}

	// Fixed number of rendered lines for stable re-rendering
	totalLines := viewSize
	if scrollable {
		totalLines += 2 // top + bottom scroll indicators
	}

	dim := lipgloss.NewStyle().Foreground(setupDim)
	cur := lipgloss.NewStyle().Foreground(setupCyan).Bold(true)
	act := lipgloss.NewStyle().Foreground(setupCyan)
	inact := lipgloss.NewStyle().Foreground(setupGray)

	// Label line (printed once, above the redrawn region)
	fmt.Printf("  %s  %s\r\n", label, dim.Render("↑/↓ select, Enter confirm"))

	render := func(first bool) {
		if !first {
			fmt.Printf("\x1b[%dA", totalLines)
		}

		if scrollable {
			fmt.Print("\x1b[2K")
			if above := offset; above > 0 {
				fmt.Printf("    %s\r\n", dim.Render(fmt.Sprintf("↑ %d more", above)))
			} else {
				fmt.Print("\r\n")
			}
		}

		for i := offset; i < offset+viewSize && i < len(options); i++ {
			fmt.Print("\x1b[2K")
			if i == selected {
				fmt.Printf("    %s %s\r\n", cur.Render("›"), act.Render(options[i]))
			} else {
				fmt.Printf("      %s\r\n", inact.Render(options[i]))
			}
		}

		if scrollable {
			fmt.Print("\x1b[2K")
			if below := len(options) - offset - viewSize; below > 0 {
				fmt.Printf("    %s\r\n", dim.Render(fmt.Sprintf("↓ %d more", below)))
			} else {
				fmt.Print("\r\n")
			}
		}
	}

	render(true)

	// Read input
	buf := make([]byte, 3)
	for {
		n, readErr := os.Stdin.Read(buf[:1])
		if readErr != nil || n == 0 {
			break
		}

		switch buf[0] {
		case '\r', '\n': // Enter
			// Collapse list into single result line
			_ = term.Restore(fd, oldState)
			fmt.Printf("\x1b[%dA", totalLines+1) // move up past list + label
			fmt.Print("\x1b[J")                   // clear to end of screen
			fmt.Printf("  %s: %s\n", label, act.Render(options[selected]))
			return options[selected]

		case 3: // Ctrl+C
			_ = term.Restore(fd, oldState)
			fmt.Print("\r\n")
			os.Exit(1)

		case 'j': // vim down
			if selected < len(options)-1 {
				selected++
			}
			adjustScroll()
			render(false)

		case 'k': // vim up
			if selected > 0 {
				selected--
			}
			adjustScroll()
			render(false)

		case '\x1b': // Escape sequence
			n2, _ := os.Stdin.Read(buf[1:3])
			if n2 == 2 && len(buf) > 2 && buf[1] == '[' {
				switch buf[2] {
				case 'A': // Up
					if selected > 0 {
						selected--
					}
				case 'B': // Down
					if selected < len(options)-1 {
						selected++
					}
				}
				adjustScroll()
				render(false)
			}
		}
	}

	_ = term.Restore(fd, oldState)
	return options[selected]
}

// promptSelectFallback is a numbered-input fallback when raw mode is unavailable.
func promptSelectFallback(label string, options []string, defaultIdx int) string {
	num := lipgloss.NewStyle().Foreground(setupCyan)
	dim := lipgloss.NewStyle().Foreground(setupDim)

	fmt.Printf("  %s:\n", label)
	for i, opt := range options {
		n := num.Render(fmt.Sprintf("%d)", i+1))
		if i == defaultIdx {
			fmt.Printf("    %s %s %s\n", n, opt, dim.Render("(default)"))
		} else {
			fmt.Printf("    %s %s\n", n, opt)
		}
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("  Choice [%s]: ", num.Render(strconv.Itoa(defaultIdx+1)))
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return options[defaultIdx]
	}
	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(options) {
		return options[defaultIdx]
	}
	return options[idx-1]
}

// findOption returns the index of val in options, or fallback if not found.
func findOption(options []string, val string, fallback int) int {
	for i, opt := range options {
		if opt == val {
			return i
		}
	}
	return fallback
}

// runInteractiveSetup runs the interactive setup with styled output.
// firstRun=true shows "First-time setup"; false shows "Reconfigure".
func runInteractiveSetup(firstRun bool) {
	// Logo
	logo := lipgloss.NewStyle().Foreground(setupCyan).Render(assets.BootLogo)
	fmt.Println(logo)
	fmt.Println()

	// Title lines
	subtitle := "Reconfigure"
	if firstRun {
		subtitle = "First-time setup"
	}
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(setupGreen).Render("     Private LLM Agent"))
	fmt.Println(lipgloss.NewStyle().Foreground(setupGray).Render("     " + subtitle))
	fmt.Println(lipgloss.NewStyle().Foreground(setupDim).Render("     Press Enter to accept defaults"))

	// ── Project ──────────────────────────
	sectionHeader("Project")

	// cfg.ProjectID may already be set by inferProjectID() in main.go
	if cfg.ProjectID != "" {
		msg := fmt.Sprintf("  \u2713 Detected project from gcloud: %s", cfg.ProjectID)
		fmt.Println(lipgloss.NewStyle().Foreground(setupGreen).Render(msg))
	}

	for {
		cfg.ProjectID = promptString("GCP Project ID", cfg.ProjectID)
		if cfg.ProjectID != "" {
			break
		}
		fmt.Println(lipgloss.NewStyle().Foreground(setupRed).Render("  Project ID is required"))
	}

	// ── Compute ──────────────────────────
	sectionHeader("Compute")

	mtDefault := orDefault(cfg.MachineType, "g2-standard-48")
	cfg.MachineType = promptSelect("Machine type", machineTypeOptions, findOption(machineTypeOptions, mtDefault, 0))

	// Zone list filtered by selected machine type
	zones := zonesForMachineType(cfg.MachineType)
	zoneDefault := orDefault(cfg.Zone, "us-central1-a")
	cfg.Zone = promptSelect("Zone", zones, findOption(zones, zoneDefault, 0))

	cfg.VMName = promptString("VM name", orDefault(cfg.VMName, "private-llm-vm"))
	cfg.DefaultModel = promptString("Default model", orDefault(cfg.DefaultModel, "qwen3.5:122b"))
	cfg.ContextLength = promptInt("Context length", orDefaultInt(cfg.ContextLength, 262144))
	cfg.IdleTimeout = promptInt("Idle timeout (seconds)", orDefaultInt(cfg.IdleTimeout, 300))

	// ── Networking ───────────────────────
	sectionHeader("Networking")

	cfg.Network = promptString("VPC network", orDefault(cfg.Network, "private-llm"))
	cfg.Subnet = promptString("Subnet name", orDefault(cfg.Subnet, "private-llm-subnet"))
	cfg.SubnetCIDR = promptString("Subnet CIDR", orDefault(cfg.SubnetCIDR, "10.10.0.0/24"))

	// ── Listening ────────────────────────
	sectionHeader("Listening")

	cfg.ListenAddr = promptString("Listen address", orDefault(cfg.ListenAddr, "127.0.0.1"))

	// ── Security ─────────────────────────
	sectionHeader("Security")

	hsmOptions := []string{"Yes", "No"}
	hsmDefault := 0
	if cfg.DisableHSM {
		hsmDefault = 1
	}
	cfg.DisableHSM = promptSelect("HSM encryption", hsmOptions, hsmDefault) == "No"

	fmt.Println()
}
