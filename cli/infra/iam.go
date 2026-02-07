package infra

import (
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/projects"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/serviceaccount"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func provisionIAM(ctx *pulumi.Context, cfg *InfraConfig) (*IAMResult, error) {
	vmSA, err := serviceaccount.NewAccount(ctx, "vm-sa", &serviceaccount.AccountArgs{
		AccountId:   pulumi.String("private-llm-vm"),
		DisplayName: pulumi.String("Private LLM VM"),
		Project:     pulumi.String(cfg.ProjectID),
	})
	if err != nil {
		return nil, err
	}

	// VM logging
	_, err = projects.NewIAMMember(ctx, "vm-logging", &projects.IAMMemberArgs{
		Project: pulumi.String(cfg.ProjectID),
		Role:    pulumi.String("roles/logging.logWriter"),
		Member:  pulumi.Sprintf("serviceAccount:%s", vmSA.Email),
	})
	if err != nil {
		return nil, err
	}

	// VM monitoring
	_, err = projects.NewIAMMember(ctx, "vm-monitoring", &projects.IAMMemberArgs{
		Project: pulumi.String(cfg.ProjectID),
		Role:    pulumi.String("roles/monitoring.metricWriter"),
		Member:  pulumi.Sprintf("serviceAccount:%s", vmSA.Email),
	})
	if err != nil {
		return nil, err
	}

	return &IAMResult{VMSA: vmSA}, nil
}
