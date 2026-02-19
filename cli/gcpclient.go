package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"

	compute "cloud.google.com/go/compute/apiv1"
	"google.golang.org/api/googleapi"
)

// gcpClients lazily creates and caches GCP compute clients to avoid
// hitting OAuth2 token endpoints on every poll tick (~720/hour).
type gcpClients struct {
	mu        sync.Mutex
	instances *compute.InstancesClient
	firewalls *compute.FirewallsClient
}

// gcp is the global cached client pool, used by vm.go and firewall.go.
var gcp gcpClients

// Instances returns a cached InstancesClient, creating it on first call.
func (g *gcpClients) Instances(ctx context.Context) (*compute.InstancesClient, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.instances != nil {
		return g.instances, nil
	}

	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	log.Printf("[gcpclient] created instances client")
	g.instances = client
	return client, nil
}

// Firewalls returns a cached FirewallsClient, creating it on first call.
func (g *gcpClients) Firewalls(ctx context.Context) (*compute.FirewallsClient, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.firewalls != nil {
		return g.firewalls, nil
	}

	client, err := compute.NewFirewallsRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	log.Printf("[gcpclient] created firewalls client")
	g.firewalls = client
	return client, nil
}

// Close closes all cached clients. Safe to call multiple times.
func (g *gcpClients) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.instances != nil {
		_ = g.instances.Close()
		g.instances = nil
	}
	if g.firewalls != nil {
		_ = g.firewalls.Close()
		g.firewalls = nil
	}
}

// isAuthError returns true if the error indicates expired or invalid GCP credentials.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}

	var gerr *googleapi.Error
	if errors.As(err, &gerr) && (gerr.Code == 401 || gerr.Code == 403) {
		return true
	}

	msg := err.Error()
	return strings.Contains(msg, "oauth2: cannot fetch token") ||
		strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "token expired") ||
		strings.Contains(msg, "credentials")
}
