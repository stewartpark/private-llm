package infra

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/compute"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/serviceaccount"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func provisionCompute(ctx *pulumi.Context, cfg *InfraConfig, net *NetworkResult, vmSA *serviceaccount.Account) error {
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
		BootDisk: &compute.InstanceBootDiskArgs{
			InitializeParams: &compute.InstanceBootDiskInitializeParamsArgs{
				Image:                 pulumi.String("projects/deeplearning-platform-release/global/images/family/common-cu128-ubuntu-2404-nvidia-570"),
				Size:                  pulumi.Int(128),
				Type:                  pulumi.String("hyperdisk-balanced"),
				ProvisionedIops:       pulumi.Int(3000),
				ProvisionedThroughput: pulumi.Int(700),
			},
		},
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
	})

	return err
}
