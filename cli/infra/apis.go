package infra

import (
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/projects"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// APIsResult holds API enablement resources for dependency wiring.
type APIsResult struct {
	Compute       *projects.Service
	SecretManager *projects.Service
	CloudKMS      *projects.Service // nil when DisableHSM
}

func provisionAPIs(ctx *pulumi.Context, cfg *InfraConfig) (*APIsResult, error) {
	result := &APIsResult{}

	computeAPI, err := projects.NewService(ctx, "api-compute", &projects.ServiceArgs{
		Project:          pulumi.String(cfg.ProjectID),
		Service:          pulumi.String("compute.googleapis.com"),
		DisableOnDestroy: pulumi.Bool(false),
	})
	if err != nil {
		return nil, err
	}
	result.Compute = computeAPI

	smAPI, err := projects.NewService(ctx, "api-secretmanager", &projects.ServiceArgs{
		Project:          pulumi.String(cfg.ProjectID),
		Service:          pulumi.String("secretmanager.googleapis.com"),
		DisableOnDestroy: pulumi.Bool(false),
	})
	if err != nil {
		return nil, err
	}
	result.SecretManager = smAPI

	_, err = projects.NewService(ctx, "api-osconfig", &projects.ServiceArgs{
		Project:          pulumi.String(cfg.ProjectID),
		Service:          pulumi.String("osconfig.googleapis.com"),
		DisableOnDestroy: pulumi.Bool(false),
	})
	if err != nil {
		return nil, err
	}

	if !cfg.DisableHSM {
		kmsAPI, err := projects.NewService(ctx, "api-cloudkms", &projects.ServiceArgs{
			Project:          pulumi.String(cfg.ProjectID),
			Service:          pulumi.String("cloudkms.googleapis.com"),
			DisableOnDestroy: pulumi.Bool(false),
		})
		if err != nil {
			return nil, err
		}
		result.CloudKMS = kmsAPI
	}

	return result, nil
}
