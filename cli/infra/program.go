package infra

import (
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/kms"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/organizations"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// DefineInfrastructure is the Pulumi program that provisions all resources.
func DefineInfrastructure(cfg *InfraConfig) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		// 1. Enable required APIs
		apis, err := provisionAPIs(ctx, cfg)
		if err != nil {
			return err
		}

		// Get project number for IAM member formatting
		project, err := organizations.LookupProject(ctx, &organizations.LookupProjectArgs{
			ProjectId: &cfg.ProjectID,
		})
		if err != nil {
			return err
		}
		projectNumber := pulumi.String(project.Number).ToStringOutput()

		// 2. Network (VPC + Subnet)
		net, err := provisionNetwork(ctx, cfg, pulumi.DependsOn([]pulumi.Resource{apis.Compute}))
		if err != nil {
			return err
		}

		// 3. KMS (optional)
		var cryptoKey *kms.CryptoKey
		if !cfg.DisableHSM {
			kmsResult, err := provisionKMS(ctx, cfg, projectNumber,
				pulumi.DependsOn([]pulumi.Resource{apis.CloudKMS, apis.SecretManager}))
			if err != nil {
				return err
			}
			cryptoKey = kmsResult.CryptoKey
		}

		// 4. IAM (VM SA + project-level IAM)
		iamResult, err := provisionIAM(ctx, cfg)
		if err != nil {
			return err
		}

		// 5. Secrets (4 secrets + initial versions + VM IAM)
		_, err = provisionSecrets(ctx, cfg, cryptoKey, iamResult.VMSA,
			pulumi.DependsOn([]pulumi.Resource{apis.SecretManager}))
		if err != nil {
			return err
		}

		// 6. Compute (VM instance)
		if err := provisionCompute(ctx, cfg, net, iamResult.VMSA,
			pulumi.DependsOn([]pulumi.Resource{apis.Compute})); err != nil {
			return err
		}

		return nil
	}
}
