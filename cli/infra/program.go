package infra

import (
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/organizations"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// DefineInfrastructure is the Pulumi program that provisions all resources.
func DefineInfrastructure(cfg *InfraConfig) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		// 1. Enable required APIs
		if err := provisionAPIs(ctx, cfg); err != nil {
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
		net, err := provisionNetwork(ctx, cfg)
		if err != nil {
			return err
		}

		// 3. KMS (Identity + KeyRing + Key + IAM)
		kmsResult, err := provisionKMS(ctx, cfg, projectNumber)
		if err != nil {
			return err
		}

		// 4. IAM (VM SA + project-level IAM)
		iamResult, err := provisionIAM(ctx, cfg)
		if err != nil {
			return err
		}

		// 5. Secrets (4 secrets + initial versions + VM IAM)
		_, err = provisionSecrets(ctx, cfg, kmsResult.CryptoKey, iamResult.VMSA)
		if err != nil {
			return err
		}

		// 6. Compute (VM instance)
		if err := provisionCompute(ctx, cfg, net, iamResult.VMSA); err != nil {
			return err
		}

		return nil
	}
}
