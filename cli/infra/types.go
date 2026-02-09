package infra

import (
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/compute"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/kms"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/secretmanager"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/serviceaccount"
)

// InfraConfig holds all parameters needed to provision infrastructure.
type InfraConfig struct {
	ProjectID     string
	Region        string
	Zone          string
	VMName        string
	Network       string
	MachineType   string
	DefaultModel  string
	ContextLength int
	IdleTimeout   int
	SubnetCIDR    string
	Subnet        string
	DisableHSM    bool
	// Embedded content for VM metadata
	StartupScript string
	Caddyfile     string
}

// NetworkResult holds provisioned network resources.
type NetworkResult struct {
	VPC    *compute.Network
	Subnet *compute.Subnetwork
}

// KMSResult holds provisioned KMS resources.
type KMSResult struct {
	KeyRing   *kms.KeyRing
	CryptoKey *kms.CryptoKey
}

// SecretsResult holds provisioned secret resources.
type SecretsResult struct {
	CACert   *secretmanager.Secret
	ServerCert *secretmanager.Secret
	ServerKey  *secretmanager.Secret
	Token      *secretmanager.Secret
}

// IAMResult holds provisioned IAM resources.
type IAMResult struct {
	VMSA *serviceaccount.Account
}
