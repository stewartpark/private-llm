package infra

import (
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/projects"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func provisionAPIs(ctx *pulumi.Context, cfg *InfraConfig) error {
	apis := []struct {
		name    string
		service string
	}{
		{"api-compute", "compute.googleapis.com"},
		{"api-secretmanager", "secretmanager.googleapis.com"},
		{"api-cloudkms", "cloudkms.googleapis.com"},
		{"api-osconfig", "osconfig.googleapis.com"},
	}

	for _, api := range apis {
		_, err := projects.NewService(ctx, api.name, &projects.ServiceArgs{
			Project:          pulumi.String(cfg.ProjectID),
			Service:          pulumi.String(api.service),
			DisableOnDestroy: pulumi.Bool(false),
		})
		if err != nil {
			return err
		}
	}

	return nil
}
