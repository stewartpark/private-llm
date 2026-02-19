package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
)

var firewallRuleName = "private-llm-agent"

var (
	cachedPublicIP     string
	cachedPublicIPMu   sync.RWMutex
	firewallAllowAll   bool
	firewallIsActive   bool
	firewallIsActiveMu sync.RWMutex
)

// IsFirewallActive returns whether the dynamic firewall rule is currently active.
func IsFirewallActive() bool {
	firewallIsActiveMu.RLock()
	defer firewallIsActiveMu.RUnlock()
	return firewallIsActive
}

// GetCachedPublicIP returns the last detected public IP.
func GetCachedPublicIP() string {
	cachedPublicIPMu.RLock()
	defer cachedPublicIPMu.RUnlock()
	return cachedPublicIP
}

// detectPublicIP fetches the user's current public IP address.
func detectPublicIP() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "", fmt.Errorf("failed to detect public IP: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read IP response: %w", err)
	}

	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return "", fmt.Errorf("empty IP response")
	}
	return ip, nil
}

// ensureFirewallOpen creates or updates the dynamic firewall rule to allow
// the user's current public IP to reach TCP 8080 on VMs tagged private-llm.
func ensureFirewallOpen(ctx context.Context) error {
	var sourceRange string
	if firewallAllowAll {
		sourceRange = "0.0.0.0/0"
		log.Printf("[firewall] allowing all IPs")
	} else {
		publicIP, err := detectPublicIP()
		if err != nil {
			return err
		}
		cachedPublicIPMu.Lock()
		cachedPublicIP = publicIP
		cachedPublicIPMu.Unlock()
		sourceRange = publicIP + "/32"
		log.Printf("[firewall] public IP: %s", publicIP)
	}

	client, err := gcp.Firewalls(ctx)
	if err != nil {
		return fmt.Errorf("failed to create firewall client: %w", err)
	}

	// Try to get existing rule
	existing, err := client.Get(ctx, &computepb.GetFirewallRequest{
		Project:  cfg.ProjectID,
		Firewall: firewallRuleName,
	})
	if err != nil {
		// Check if 404 (not found) - need to create
		var gerr *googleapi.Error
		if errors.As(err, &gerr) && gerr.Code == 404 {
			return createFirewallRule(ctx, sourceRange)
		}
		return fmt.Errorf("failed to get firewall rule: %w", err)
	}

	// Rule exists - check if source range matches
	ranges := existing.GetSourceRanges()
	if len(ranges) == 1 && ranges[0] == sourceRange {
		log.Printf("[firewall] rule already allows %s", sourceRange)
		firewallIsActiveMu.Lock()
		firewallIsActive = true
		firewallIsActiveMu.Unlock()
		return nil
	}

	// Update with correct IP
	log.Printf("[firewall] updating rule to allow %s", sourceRange)
	op, err := client.Patch(ctx, &computepb.PatchFirewallRequest{
		Project:  cfg.ProjectID,
		Firewall: firewallRuleName,
		FirewallResource: &computepb.Firewall{
			SourceRanges: []string{sourceRange},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update firewall rule: %w", err)
	}
	_ = op
	log.Printf("[firewall] rule updated")
	firewallIsActiveMu.Lock()
	firewallIsActive = true
	firewallIsActiveMu.Unlock()
	return nil
}

// createFirewallRule creates the dynamic firewall rule.
func createFirewallRule(ctx context.Context, sourceRange string) error {
	log.Printf("[firewall] creating rule %s for %s", firewallRuleName, sourceRange)

	client, err := gcp.Firewalls(ctx)
	if err != nil {
		return fmt.Errorf("failed to create firewall client: %w", err)
	}

	priority := int32(900)
	direction := "INGRESS"
	network := fmt.Sprintf("projects/%s/global/networks/%s", cfg.ProjectID, cfg.Network)

	op, err := client.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: cfg.ProjectID,
		FirewallResource: &computepb.Firewall{
			Name:         &firewallRuleName,
			Network:      &network,
			Direction:    &direction,
			Priority:     &priority,
			SourceRanges: []string{sourceRange},
			Allowed: []*computepb.Allowed{
				{
					IPProtocol: strPtr("tcp"),
					Ports:      []string{"8080"},
				},
			},
			TargetTags: []string{"private-llm"},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create firewall rule: %w", err)
	}
	_ = op
	log.Printf("[firewall] rule created")
	firewallIsActiveMu.Lock()
	firewallIsActive = true
	firewallIsActiveMu.Unlock()
	return nil
}

// removeFirewall deletes the dynamic firewall rule (called on shutdown).
// If the rule does not exist (404), it logs and returns.
func removeFirewall(ctx context.Context) {
	log.Printf("[firewall] removing rule %s...", firewallRuleName)

	client, err := gcp.Firewalls(ctx)
	if err != nil {
		log.Printf("[firewall] failed to create client for cleanup: %v", err)
		return
	}

	// Check if rule exists before attempting delete
	_, err = client.Get(ctx, &computepb.GetFirewallRequest{
		Project:  cfg.ProjectID,
		Firewall: firewallRuleName,
	})
	if err != nil {
		var gerr *googleapi.Error
		if errors.As(err, &gerr) && gerr.Code == 404 {
			log.Printf("[firewall] rule already removed (not found)")
			firewallIsActiveMu.Lock()
			firewallIsActive = false
			firewallIsActiveMu.Unlock()
			return
		}
		log.Printf("[firewall] failed to check rule: %v", err)
		return
	}

	_, err = client.Delete(ctx, &computepb.DeleteFirewallRequest{
		Project:  cfg.ProjectID,
		Firewall: firewallRuleName,
	})
	if err != nil {
		log.Printf("[firewall] failed to delete rule: %v", err)
	} else {
		log.Printf("[firewall] rule deleted")
	}
	firewallIsActiveMu.Lock()
	firewallIsActive = false
	firewallIsActiveMu.Unlock()
}

func strPtr(s string) *string {
	return &s
}
