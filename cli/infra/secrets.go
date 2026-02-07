package infra

import (
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/kms"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/secretmanager"
	"github.com/pulumi/pulumi-gcp/sdk/v8/go/gcp/serviceaccount"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func provisionSecrets(ctx *pulumi.Context, cfg *InfraConfig, cryptoKey *kms.CryptoKey, vmSA *serviceaccount.Account) (*SecretsResult, error) {
	secretIDs := []struct {
		name     string
		secretID string
	}{
		{"secret-ca-cert", "private-llm-ca-cert"},
		{"secret-server-cert", "private-llm-server-cert"},
		{"secret-server-key", "private-llm-server-key"},
		{"secret-token", "private-llm-internal-token"},
	}

	secrets := make(map[string]*secretmanager.Secret)
	for _, s := range secretIDs {
		secret, err := secretmanager.NewSecret(ctx, s.name, &secretmanager.SecretArgs{
			SecretId: pulumi.String(s.secretID),
			Project:  pulumi.String(cfg.ProjectID),
			Replication: &secretmanager.SecretReplicationArgs{
				UserManaged: &secretmanager.SecretReplicationUserManagedArgs{
					Replicas: secretmanager.SecretReplicationUserManagedReplicaArray{
						&secretmanager.SecretReplicationUserManagedReplicaArgs{
							Location: pulumi.String(cfg.Region),
							CustomerManagedEncryption: &secretmanager.SecretReplicationUserManagedReplicaCustomerManagedEncryptionArgs{
								KmsKeyName: cryptoKey.ID(),
							},
						},
					},
				},
			},
			VersionDestroyTtl: pulumi.String("2592000s"), // 30 days
		})
		if err != nil {
			return nil, err
		}
		secrets[s.secretID] = secret

		// VM service account can read this secret
		_, err = secretmanager.NewSecretIamMember(ctx, s.name+"-vm-access", &secretmanager.SecretIamMemberArgs{
			SecretId: secret.SecretId,
			Project:  pulumi.String(cfg.ProjectID),
			Role:     pulumi.String("roles/secretmanager.secretAccessor"),
			Member:   pulumi.Sprintf("serviceAccount:%s", vmSA.Email),
		})
		if err != nil {
			return nil, err
		}
	}

	return &SecretsResult{
		CACert:     secrets["private-llm-ca-cert"],
		ServerCert: secrets["private-llm-server-cert"],
		ServerKey:  secrets["private-llm-server-key"],
		Token:      secrets["private-llm-internal-token"],
	}, nil
}
