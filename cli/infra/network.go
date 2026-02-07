package infra

import (
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/compute"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func provisionNetwork(ctx *pulumi.Context, cfg *InfraConfig) (*NetworkResult, error) {
	vpc, err := compute.NewNetwork(ctx, "vpc", &compute.NetworkArgs{
		Name:                  pulumi.String(cfg.Network),
		Project:               pulumi.String(cfg.ProjectID),
		AutoCreateSubnetworks: pulumi.Bool(false),
	})
	if err != nil {
		return nil, err
	}

	subnet, err := compute.NewSubnetwork(ctx, "subnet", &compute.SubnetworkArgs{
		Name:                   pulumi.String("private-llm-subnet"),
		IpCidrRange:            pulumi.String(cfg.SubnetCIDR),
		Region:                 pulumi.String(cfg.Region),
		Network:                vpc.ID(),
		Project:                pulumi.String(cfg.ProjectID),
		PrivateIpGoogleAccess:  pulumi.Bool(true),
	})
	if err != nil {
		return nil, err
	}

	return &NetworkResult{VPC: vpc, Subnet: subnet}, nil
}
