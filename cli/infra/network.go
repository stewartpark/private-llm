package infra

import (
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/compute"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func provisionNetwork(ctx *pulumi.Context, cfg *InfraConfig, opts ...pulumi.ResourceOption) (*NetworkResult, error) {
	vpc, err := compute.NewNetwork(ctx, "vpc", &compute.NetworkArgs{
		Name:                  pulumi.String(cfg.Network),
		Project:               pulumi.String(cfg.ProjectID),
		AutoCreateSubnetworks: pulumi.Bool(false),
	}, opts...)
	if err != nil {
		return nil, err
	}

	subnet, err := compute.NewSubnetwork(ctx, "subnet", &compute.SubnetworkArgs{
		Name:                   pulumi.String(cfg.Subnet),
		IpCidrRange:            pulumi.String(cfg.SubnetCIDR),
		Region:                 pulumi.String(cfg.Region),
		Network:                vpc.ID(),
		Project:                pulumi.String(cfg.ProjectID),
		PrivateIpGoogleAccess:  pulumi.Bool(true),
	}, opts...)
	if err != nil {
		return nil, err
	}

	return &NetworkResult{VPC: vpc, Subnet: subnet}, nil
}
