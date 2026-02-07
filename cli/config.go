package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Config holds agent configuration loaded from agent.json.
type Config struct {
	ProjectID     string `json:"project_id"`
	Zone          string `json:"zone"`
	VMName        string `json:"vm_name"`
	Network       string `json:"network"`
	Region        string `json:"region"`
	MachineType   string `json:"machine_type"`
	DefaultModel  string `json:"default_model"`
	ContextLength int    `json:"context_length"`
	IdleTimeout   int    `json:"idle_timeout"`
	SubnetCIDR    string `json:"subnet_cidr"`
}

var cfg Config

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "private-llm", "agent.json")
}

func configPathOrDefault(path string) string {
	if path == "" {
		return defaultConfigPath()
	}
	return path
}

// loadConfig loads and validates config from file. Fails if file is missing.
func loadConfig(path string) error {
	path = configPathOrDefault(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config not found: %s\nRun 'private-llm up' to set up infrastructure", path)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	applyDefaults()

	if cfg.ProjectID == "" || cfg.Zone == "" {
		return fmt.Errorf("config must include project_id and zone")
	}

	return nil
}

// loadConfigFile loads config from file if it exists. Returns true if loaded.
func loadConfigFile(path string) bool {
	path = configPathOrDefault(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	return true
}

// applyDefaults fills in default values for empty config fields.
func applyDefaults() {
	if cfg.Zone == "" {
		cfg.Zone = "us-central1-a"
	}
	if cfg.VMName == "" {
		cfg.VMName = "private-llm-vm"
	}
	if cfg.Network == "" {
		cfg.Network = "private-llm"
	}
	if cfg.Region == "" {
		parts := strings.Split(cfg.Zone, "-")
		if len(parts) >= 3 {
			cfg.Region = strings.Join(parts[:len(parts)-1], "-")
		} else {
			cfg.Region = "us-central1"
		}
	}
	if cfg.MachineType == "" {
		cfg.MachineType = "g4-standard-48"
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "qwen3-coder-next:q8_0"
	}
	if cfg.ContextLength == 0 {
		cfg.ContextLength = 262144
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 300
	}
	if cfg.SubnetCIDR == "" {
		cfg.SubnetCIDR = "10.10.0.0/24"
	}
}

// inferProjectID attempts to get the GCP project ID from gcloud config.
func inferProjectID() string {
	cmd := exec.Command("gcloud", "config", "get-value", "project")
	cmd.Stderr = nil
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(output))
	if p == "" || p == "(unset)" {
		return ""
	}
	return p
}

// saveConfig writes the current config to the config file.
func saveConfig(path string) error {
	path = configPathOrDefault(path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// promptString prompts the user for a string value with a default.
func promptString(label, defaultVal string) string {
	reader := bufio.NewReader(os.Stdin)
	if defaultVal != "" {
		fmt.Printf("  %s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("  %s: ", label)
	}
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	return input
}

// CertsDir returns the local certs directory path.
func CertsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "private-llm", "certs")
}

// StateDir returns the local Pulumi state directory path.
func StateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "private-llm", "state")
}
