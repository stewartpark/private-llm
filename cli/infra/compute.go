package infra

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/compute"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/serviceaccount"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// machineFamily extracts the family prefix (e.g. "g2") from a machine type like "g2-standard-8".
func machineFamily(machineType string) string {
	parts := strings.SplitN(machineType, "-", 2)
	if len(parts) > 0 {
		return strings.ToLower(parts[0])
	}
	return ""
}

// supportsHyperdisk returns true if the machine type family supports hyperdisk-balanced.
func supportsHyperdisk(machineType string) bool {
	switch machineFamily(machineType) {
	case "c3", "c3d", "c4", "c4a", "m3", "n4", "z3", "a3", "g4", "h3":
		return true
	default:
		return false
	}
}

func provisionCompute(ctx *pulumi.Context, cfg *InfraConfig, net *NetworkResult, vmSA *serviceaccount.Account, opts ...pulumi.ResourceOption) error {
	_, err := compute.NewInstance(ctx, "vm", &compute.InstanceArgs{
		Name:                    pulumi.String(cfg.VMName),
		MachineType:             pulumi.String(cfg.MachineType),
		Zone:                    pulumi.String(cfg.Zone),
		Project:                 pulumi.String(cfg.ProjectID),
		AllowStoppingForUpdate:  pulumi.Bool(true),
		Scheduling: &compute.InstanceSchedulingArgs{
			ProvisioningModel:         pulumi.String("SPOT"),
			Preemptible:               pulumi.Bool(true),
			AutomaticRestart:          pulumi.Bool(false),
			InstanceTerminationAction: pulumi.String("STOP"),
			OnHostMaintenance:         pulumi.String("TERMINATE"),
		},
		BootDisk: bootDiskArgs(cfg.MachineType),
		ShieldedInstanceConfig: &compute.InstanceShieldedInstanceConfigArgs{
			EnableSecureBoot:         pulumi.Bool(true),
			EnableVtpm:               pulumi.Bool(true),
			EnableIntegrityMonitoring: pulumi.Bool(true),
		},
		NetworkInterfaces: compute.InstanceNetworkInterfaceArray{
			&compute.InstanceNetworkInterfaceArgs{
				Network:    net.VPC.Name,
				Subnetwork: net.Subnet.Name,
				AccessConfigs: compute.InstanceNetworkInterfaceAccessConfigArray{
					&compute.InstanceNetworkInterfaceAccessConfigArgs{},
				},
			},
		},
		Tags: pulumi.StringArray{pulumi.String("private-llm")},
		ServiceAccount: &compute.InstanceServiceAccountArgs{
			Email: vmSA.Email,
			Scopes: pulumi.StringArray{
				pulumi.String("https://www.googleapis.com/auth/monitoring.write"),
				pulumi.String("https://www.googleapis.com/auth/cloud-platform"),
			},
		},
		Metadata: pulumi.StringMap{
			"caddyfile":               pulumi.String(cfg.Caddyfile),
			"context-length":          pulumi.String(fmt.Sprintf("%d", cfg.ContextLength)),
			"model":                   pulumi.String(cfg.DefaultModel),
			"idle-timeout":            pulumi.String(fmt.Sprintf("%d", cfg.IdleTimeout)),
			"enable-osconfig":         pulumi.String("TRUE"),
			"enable-guest-attributes": pulumi.String("TRUE"),
			"startup-script":          pulumi.String(cfg.StartupScript),
		},
	}, opts...)

	return err
}

func bootDiskArgs(machineType string) *compute.InstanceBootDiskArgs {
	diskImage := "projects/deeplearning-platform-release/global/images/family/common-cu128-ubuntu-2404-nvidia-570"
	params := &compute.InstanceBootDiskInitializeParamsArgs{
		Image: pulumi.String(diskImage),
		Size:  pulumi.Int(128),
	}
	if supportsHyperdisk(machineType) {
		params.Type = pulumi.String("hyperdisk-balanced")
		params.ProvisionedIops = pulumi.Int(3000)
		params.ProvisionedThroughput = pulumi.Int(700)
	} else {
		params.Type = pulumi.String("pd-ssd")
	}
	return &compute.InstanceBootDiskArgs{InitializeParams: params}
}
