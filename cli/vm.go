package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
)

var (
	cachedExternalIP string
	cachedIPMu       sync.RWMutex
)

// getExternalIP extracts the external IP from a VM instance.
// If instance is provided, caches and returns it. Otherwise returns cached value.
func getExternalIP(instance *computepb.Instance) string {
	if instance != nil {
		ifaces := instance.GetNetworkInterfaces()
		if len(ifaces) > 0 {
			configs := ifaces[0].GetAccessConfigs()
			if len(configs) > 0 {
				ip := configs[0].GetNatIP()
				if ip != "" {
					log.Printf("[vm] external IP: %s", ip)
					cachedIPMu.Lock()
					cachedExternalIP = ip
					cachedIPMu.Unlock()
					return ip
				}
			}
		}
	}
	cachedIPMu.RLock()
	defer cachedIPMu.RUnlock()
	return cachedExternalIP
}

// isVMStopped checks if the VM is in a stopped/terminated state (needs starting).
func isVMStopped(ctx context.Context) (bool, error) {
	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	instance, err := client.Get(ctx, &computepb.GetInstanceRequest{
		Project:  cfg.ProjectID,
		Zone:     cfg.Zone,
		Instance: cfg.VMName,
	})
	if err != nil {
		return false, fmt.Errorf("failed to get instance: %w", err)
	}

	status := instance.GetStatus()
	return status == "TERMINATED" || status == "STOPPED" || status == "SUSPENDED", nil
}

// ensureVMRunning starts the VM if it's not running and waits for Ollama to be ready.
// Returns the VM's external IP.
func ensureVMRunning(ctx context.Context) (string, error) {
	log.Printf("[vm] ensuring VM running (project=%s, zone=%s, vm=%s)", cfg.ProjectID, cfg.Zone, cfg.VMName)

	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	instance, err := client.Get(ctx, &computepb.GetInstanceRequest{
		Project:  cfg.ProjectID,
		Zone:     cfg.Zone,
		Instance: cfg.VMName,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get instance: %w", err)
	}

	status := instance.GetStatus()
	log.Printf("[vm] status: %s", status)

	// Wait for transitional states
	for status == "STOPPING" || status == "STAGING" || status == "SUSPENDING" {
		log.Printf("[vm] transitional state %s, waiting...", status)
		time.Sleep(5 * time.Second)
		instance, err = client.Get(ctx, &computepb.GetInstanceRequest{
			Project:  cfg.ProjectID,
			Zone:     cfg.Zone,
			Instance: cfg.VMName,
		})
		if err != nil {
			return "", fmt.Errorf("failed to get instance: %w", err)
		}
		status = instance.GetStatus()
		log.Printf("[vm] status: %s", status)
	}

	if status == "RUNNING" {
		ip := getExternalIP(instance)
		if ip == "" {
			return "", fmt.Errorf("VM running but no external IP (enable_external_ip must be true)")
		}
		log.Printf("[vm] VM running at %s, waiting for Ollama...", ip)
		if err := waitForOllama(ctx, ip); err != nil {
			return "", err
		}
		return ip, nil
	}

	if status == "TERMINATED" || status == "STOPPED" || status == "SUSPENDED" {
		log.Printf("[vm] starting VM...")
		_, err := client.Start(ctx, &computepb.StartInstanceRequest{
			Project:  cfg.ProjectID,
			Zone:     cfg.Zone,
			Instance: cfg.VMName,
		})
		if err != nil {
			return "", fmt.Errorf("failed to start instance: %w", err)
		}

		// Wait for RUNNING + external IP assigned
		var ip string
		for i := 0; i < 60; i++ {
			time.Sleep(5 * time.Second)
			instance, err = client.Get(ctx, &computepb.GetInstanceRequest{
				Project:  cfg.ProjectID,
				Zone:     cfg.Zone,
				Instance: cfg.VMName,
			})
			if err != nil {
				continue
			}
			if instance.GetStatus() == "RUNNING" {
				ip = getExternalIP(instance)
				if ip != "" {
					break
				}
				log.Printf("[vm] RUNNING but no external IP yet, waiting...")
			}
		}
		if ip == "" {
			return "", fmt.Errorf("VM started but no external IP assigned")
		}

		log.Printf("[vm] VM started at %s, waiting for Ollama...", ip)
		if err := waitForOllama(ctx, ip); err != nil {
			return "", err
		}
		return ip, nil
	}

	return "", fmt.Errorf("VM in unexpected state: %s", status)
}

// stopVM stops the VM and waits for it to reach TERMINATED state.
func stopVM(ctx context.Context) error {
	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	instance, err := client.Get(ctx, &computepb.GetInstanceRequest{
		Project:  cfg.ProjectID,
		Zone:     cfg.Zone,
		Instance: cfg.VMName,
	})
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	status := instance.GetStatus()
	if status == "TERMINATED" || status == "STOPPED" {
		log.Printf("[vm] already stopped (%s)", status)
		return nil
	}

	log.Printf("[vm] stopping VM (status=%s)...", status)
	_, err = client.Stop(ctx, &computepb.StopInstanceRequest{
		Project:  cfg.ProjectID,
		Zone:     cfg.Zone,
		Instance: cfg.VMName,
	})
	if err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}

	for i := 0; i < 60; i++ {
		time.Sleep(5 * time.Second)
		instance, err = client.Get(ctx, &computepb.GetInstanceRequest{
			Project:  cfg.ProjectID,
			Zone:     cfg.Zone,
			Instance: cfg.VMName,
		})
		if err != nil {
			continue
		}
		status = instance.GetStatus()
		if status == "TERMINATED" || status == "STOPPED" {
			log.Printf("[vm] stopped")
			return nil
		}
		log.Printf("[vm] waiting for stop... (%s)", status)
	}
	return fmt.Errorf("timeout waiting for VM to stop")
}

// deleteVM deletes the VM instance and waits for deletion to complete.
func deleteVM(ctx context.Context) error {
	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create compute client: %w", err)
	}
	defer client.Close()

	log.Printf("[vm] deleting VM...")
	_, err = client.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project:  cfg.ProjectID,
		Zone:     cfg.Zone,
		Instance: cfg.VMName,
	})
	if err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	for i := 0; i < 60; i++ {
		time.Sleep(5 * time.Second)
		_, err = client.Get(ctx, &computepb.GetInstanceRequest{
			Project:  cfg.ProjectID,
			Zone:     cfg.Zone,
			Instance: cfg.VMName,
		})
		if err != nil {
			// Instance no longer exists
			log.Printf("[vm] deleted")
			return nil
		}
		log.Printf("[vm] waiting for deletion...")
	}
	return fmt.Errorf("timeout waiting for VM deletion")
}

// waitForOllama polls the Ollama health endpoint until it responds.
func waitForOllama(ctx context.Context, ip string) error {
	tlsCfg, token, err := getTLSConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load TLS config for health check: %w", err)
	}

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	endpoint := fmt.Sprintf("https://%s:8080/api/tags", ip)
	log.Printf("[vm] polling %s", endpoint)

	for i := 0; i < 60; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Host = "private-llm-server"
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			log.Printf("[vm] Ollama ready after %d attempts (status=%d)", i+1, resp.StatusCode)
			return nil
		}
		log.Printf("[vm] health check attempt %d: %v", i+1, err)
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for Ollama")
}


