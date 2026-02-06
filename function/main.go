package function

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/firestore"
)

var (
	projectID   = os.Getenv("GCP_PROJECT")
	zone        = os.Getenv("GCP_ZONE")
	vmName      = os.Getenv("VM_NAME")
	databaseID  = os.Getenv("FIRESTORE_DATABASE")
	idleTimeout = parseIntEnv("IDLE_TIMEOUT", 900)
)

func init() {
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if zone == "" {
		zone = "us-central1-a"
	}
	if vmName == "" {
		vmName = "private-llm-vm"
	}
	if databaseID == "" {
		databaseID = "private-llm"
	}
	if t := os.Getenv("IDLE_TIMEOUT"); t != "" {
		if v, err := strconv.Atoi(t); err == nil {
			idleTimeout = v
		}
	}
}

// VMState represents the Firestore document structure for VM state tracking
type VMState struct {
	LastRequestUnix int64 `firestore:"last_request_unix"`
	Provisioned     bool  `firestore:"provisioned"`
}

const provisioningIdleTimeout = 1800 // 30 minutes for provisioning VMs

type PubSubMessage struct {
	Data []byte `json:"data"`
}

func IdleMonitoring(ctx context.Context, m PubSubMessage) error {
	computeClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return err
	}
	defer computeClient.Close()

	instance, err := computeClient.Get(ctx, &computepb.GetInstanceRequest{
		Project:  projectID,
		Zone:     zone,
		Instance: vmName,
	})
	if err != nil {
		log.Printf("[IdleMonitoring] failed to get instance: %v", err)
		return nil
	}

	if instance.GetStatus() != "RUNNING" {
		log.Printf("[IdleMonitoring] VM not running (status: %s), skipping", instance.GetStatus())
		return nil
	}

	creationTime, err := time.Parse(time.RFC3339, *instance.CreationTimestamp)
	if err != nil {
		log.Printf("[IdleMonitoring] failed to parse creation timestamp: %v", err)
		return nil
	}

	age := time.Since(creationTime)
	log.Printf("[IdleMonitoring] VM age: %v", age)

	if age < 30*time.Minute {
		log.Printf("[IdleMonitoring] VM in honey moon period (%v old), not stopping", age)
		return nil
	}

	client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		return fmt.Errorf("failed to create Firestore client: %w", err)
	}
	defer client.Close()

	docRef := client.Collection("vm_state").Doc(vmName)
	docSnap, err := docRef.Get(ctx)
	if err != nil {
		log.Printf("[IdleMonitoring] no state document found: %v", err)
		return nil
	}

	var state VMState
	if err := docSnap.DataTo(&state); err != nil {
		return fmt.Errorf("failed to parse VM state: %w", err)
	}

	timeout := idleTimeout
	if !state.Provisioned {
		timeout = provisioningIdleTimeout
		log.Printf("[IdleMonitoring] VM provisioning, using extended timeout (%ds)", timeout)
	} else {
		log.Printf("[IdleMonitoring] VM provisioned, using standard timeout (%ds)", timeout)
	}

	var elapsed int64
	if state.LastRequestUnix == 0 {
		elapsed = 0
		log.Printf("[IdleMonitoring] last_request_unix is 0, using elapsed=0")
	} else {
		elapsed = time.Now().Unix() - state.LastRequestUnix
	}
	log.Printf("[IdleMonitoring] last request was %ds ago, timeout is %ds", elapsed, timeout)

	if elapsed < int64(timeout) {
		log.Printf("[IdleMonitoring] VM active, not stopping")
		return nil
	}

	log.Printf("[IdleMonitoring] VM idle (%ds since last request), stopping...", elapsed)

	_, err = computeClient.Stop(ctx, &computepb.StopInstanceRequest{
		Project:  projectID,
		Zone:     zone,
		Instance: vmName,
	})
	return err
}

// parseIntEnv parses an environment variable as int, returning default if not set or invalid
func parseIntEnv(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}
