package infra

import (
	"fmt"

	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/kms"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/projects"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func provisionKMS(ctx *pulumi.Context, cfg *InfraConfig, projectNumber pulumi.StringOutput) (*KMSResult, error) {
	// Create Secret Manager service identity so the SM service agent exists
	_, err := projects.NewServiceIdentity(ctx, "sm-identity", &projects.ServiceIdentityArgs{
		Project: pulumi.String(cfg.ProjectID),
		Service: pulumi.String("secretmanager.googleapis.com"),
	})
	if err != nil {
		return nil, err
	}

	keyRing, err := kms.NewKeyRing(ctx, "keyring", &kms.KeyRingArgs{
		Name:     pulumi.String("private-llm-keyring"),
		Location: pulumi.String(cfg.Region),
		Project:  pulumi.String(cfg.ProjectID),
	})
	if err != nil {
		return nil, err
	}

	cryptoKey, err := kms.NewCryptoKey(ctx, "key", &kms.CryptoKeyArgs{
		Name:           pulumi.String("private-llm-key"),
		KeyRing:        keyRing.ID(),
		RotationPeriod: pulumi.String("7776000s"), // 90 days
		Purpose:        pulumi.String("ENCRYPT_DECRYPT"),
		VersionTemplate: &kms.CryptoKeyVersionTemplateArgs{
			Algorithm:       pulumi.String("GOOGLE_SYMMETRIC_ENCRYPTION"),
			ProtectionLevel: pulumi.String("HSM"),
		},
	})
	if err != nil {
		return nil, err
	}

	// IAM: Secret Manager service agent can use the key
	_, err = kms.NewCryptoKeyIAMMember(ctx, "kms-sm-iam", &kms.CryptoKeyIAMMemberArgs{
		CryptoKeyId: cryptoKey.ID(),
		Role:        pulumi.String("roles/cloudkms.cryptoKeyEncrypterDecrypter"),
		Member: projectNumber.ApplyT(func(num string) string {
			return fmt.Sprintf("serviceAccount:service-%s@gcp-sa-secretmanager.iam.gserviceaccount.com", num)
		}).(pulumi.StringOutput),
	})
	if err != nil {
		return nil, err
	}

	return &KMSResult{KeyRing: keyRing, CryptoKey: cryptoKey}, nil
}
