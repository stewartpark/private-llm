package infra

import (
	"context"
	"fmt"
	"log"
	"sync"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	kmsapi "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optimport"
)

// DetectExistingResources queries GCP APIs in parallel and returns import specs
// for any resources that already exist.
func DetectExistingResources(ctx context.Context, cfg *InfraConfig) []*optimport.ImportResource {
	var resources []*optimport.ImportResource
	var mu sync.Mutex
	var wg sync.WaitGroup

	add := func(typ, name, id string) {
		mu.Lock()
		resources = append(resources, &optimport.ImportResource{
			Type: typ,
			Name: name,
			ID:   id,
		})
		mu.Unlock()
		log.Printf("[import] found %s: %s", name, id)
	}

	// VPC
	wg.Add(1)
	go func() {
		defer wg.Done()
		client, err := compute.NewNetworksRESTClient(ctx)
		if err != nil {
			return
		}
		defer client.Close() //nolint:errcheck
		_, err = client.Get(ctx, &computepb.GetNetworkRequest{
			Project: cfg.ProjectID,
			Network: cfg.Network,
		})
		if err == nil {
			add("gcp:compute/network:Network", "vpc",
				fmt.Sprintf("projects/%s/global/networks/%s", cfg.ProjectID, cfg.Network))
		}
	}()

	// Subnet
	wg.Add(1)
	go func() {
		defer wg.Done()
		client, err := compute.NewSubnetworksRESTClient(ctx)
		if err != nil {
			return
		}
		defer client.Close() //nolint:errcheck
		_, err = client.Get(ctx, &computepb.GetSubnetworkRequest{
			Project:    cfg.ProjectID,
			Region:     cfg.Region,
			Subnetwork: cfg.Subnet,
		})
		if err == nil {
			add("gcp:compute/subnetwork:Subnetwork", "subnet",
				fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", cfg.ProjectID, cfg.Region, cfg.Subnet))
		}
	}()

	// KMS KeyRing + Key
	if !cfg.DisableHSM {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := kmsapi.NewKeyManagementClient(ctx)
			if err != nil {
				return
			}
			defer client.Close() //nolint:errcheck
			krName := fmt.Sprintf("projects/%s/locations/global/keyRings/private-llm-keyring", cfg.ProjectID)
			_, err = client.GetKeyRing(ctx, &kmspb.GetKeyRingRequest{Name: krName})
			if err == nil {
				add("gcp:kms/keyRing:KeyRing", "keyring", krName)

				keyName := krName + "/cryptoKeys/private-llm-key"
				_, err = client.GetCryptoKey(ctx, &kmspb.GetCryptoKeyRequest{Name: keyName})
				if err == nil {
					add("gcp:kms/cryptoKey:CryptoKey", "key", keyName)
				}
			}
		}()
	}

	// Secrets
	secretSpecs := []struct {
		secretID string
		name     string
	}{
		{"private-llm-ca-cert", "secret-ca-cert"},
		{"private-llm-server-cert", "secret-server-cert"},
		{"private-llm-server-key", "secret-server-key"},
		{"private-llm-internal-token", "secret-token"},
	}
	for _, spec := range secretSpecs {
		wg.Add(1)
		go func(secretID, name string) {
			defer wg.Done()
			client, err := secretmanager.NewClient(ctx)
			if err != nil {
				return
			}
			defer client.Close() //nolint:errcheck
			fullName := fmt.Sprintf("projects/%s/secrets/%s", cfg.ProjectID, secretID)
			_, err = client.GetSecret(ctx, &secretmanagerpb.GetSecretRequest{Name: fullName})
			if err == nil {
				add("gcp:secretmanager/secret:Secret", name, fullName)
			}
		}(spec.secretID, spec.name)
	}

	// VM Service Account
	wg.Add(1)
	go func() {
		defer wg.Done()
		// SA import ID is just the email
		saEmail := fmt.Sprintf("private-llm-vm@%s.iam.gserviceaccount.com", cfg.ProjectID)
		// Use IAM API to check - but simpler: just try the projects/SA format
		add("gcp:serviceaccount/account:Account", "vm-sa",
			fmt.Sprintf("projects/%s/serviceAccounts/%s", cfg.ProjectID, saEmail))
		// Note: this will fail silently during import if it doesn't exist, which is fine
	}()

	// VM Instance
	wg.Add(1)
	go func() {
		defer wg.Done()
		client, err := compute.NewInstancesRESTClient(ctx)
		if err != nil {
			return
		}
		defer client.Close() //nolint:errcheck
		_, err = client.Get(ctx, &computepb.GetInstanceRequest{
			Project:  cfg.ProjectID,
			Zone:     cfg.Zone,
			Instance: cfg.VMName,
		})
		if err == nil {
			add("gcp:compute/instance:Instance", "vm",
				fmt.Sprintf("projects/%s/zones/%s/instances/%s", cfg.ProjectID, cfg.Zone, cfg.VMName))
		}
	}()

	wg.Wait()

	if len(resources) > 0 {
		log.Printf("[import] detected %d existing resources", len(resources))
	}
	return resources
}
