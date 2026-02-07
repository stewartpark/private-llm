package infra

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optimport"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optrefresh"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
)

const (
	projectName = "private-llm"
	stackName   = "default"
)

func getOrCreateStack(ctx context.Context, cfg *InfraConfig, stateDir string) (auto.Stack, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return auto.Stack{}, fmt.Errorf("failed to create state dir: %w", err)
	}

	backendURL := "file://" + stateDir

	project := workspace.Project{
		Name:    tokens.PackageName(projectName),
		Runtime: workspace.NewProjectRuntimeInfo("go", nil),
		Backend: &workspace.ProjectBackend{URL: backendURL},
	}

	envVars := map[string]string{
		"PULUMI_CONFIG_PASSPHRASE": "", // no encryption for local state
	}

	s, err := auto.UpsertStackInlineSource(ctx, stackName, projectName,
		DefineInfrastructure(cfg),
		auto.EnvVars(envVars),
		auto.Project(project),
	)
	if err != nil {
		return auto.Stack{}, fmt.Errorf("failed to create/select stack: %w", err)
	}

	// Set GCP config
	s.SetConfig(ctx, "gcp:project", auto.ConfigValue{Value: cfg.ProjectID})
	s.SetConfig(ctx, "gcp:region", auto.ConfigValue{Value: cfg.Region})

	return s, nil
}

// importAndRefresh detects existing GCP resources, imports them into state, and refreshes.
// Used when the stack is empty but resources may already exist in GCP.
func importAndRefresh(ctx context.Context, s auto.Stack, cfg *InfraConfig) {
	existing := DetectExistingResources(ctx, cfg)
	if len(existing) == 0 {
		log.Printf("[infra] no existing resources found")
		return
	}

	log.Printf("[infra] importing %d existing resources...", len(existing))
	_, err := s.ImportResources(ctx,
		optimport.Resources(existing),
		optimport.Protect(false),
		optimport.GenerateCode(false),
		optimport.ProgressStreams(os.Stdout),
		optimport.ErrorProgressStreams(os.Stderr),
	)
	if err != nil {
		// Import failures are non-fatal — some resources may not match or already be tracked.
		// The subsequent up/preview will handle the rest.
		log.Printf("[infra] import completed with warnings: %v", err)
	} else {
		log.Printf("[infra] import complete")
	}

	// Refresh after import to sync state with actual cloud state
	log.Printf("[infra] refreshing state after import...")
	_, err = s.Refresh(ctx, optrefresh.ProgressStreams(os.Stdout))
	if err != nil {
		log.Printf("[infra] refresh warning: %v", err)
	}
}

// refreshOrImport checks if the stack has resources and refreshes, or imports if empty.
func refreshOrImport(ctx context.Context, s auto.Stack, cfg *InfraConfig) {
	info, err := s.Info(ctx)
	if err == nil && info.ResourceCount != nil && *info.ResourceCount > 0 {
		log.Printf("[infra] refreshing state from cloud (%d resources)...", *info.ResourceCount)
		_, err = s.Refresh(ctx, optrefresh.ProgressStreams(os.Stdout))
		if err != nil {
			log.Printf("[infra] refresh warning: %v", err)
		}
	} else {
		log.Printf("[infra] empty stack, checking for existing GCP resources...")
		importAndRefresh(ctx, s, cfg)
	}
}

// Up provisions or reconciles infrastructure.
func Up(ctx context.Context, cfg *InfraConfig, stateDir string) error {
	s, err := getOrCreateStack(ctx, cfg, stateDir)
	if err != nil {
		return err
	}

	refreshOrImport(ctx, s, cfg)

	log.Printf("[infra] running up...")
	result, err := s.Up(ctx, optup.ProgressStreams(os.Stdout))
	if err != nil {
		return fmt.Errorf("pulumi up failed: %w", err)
	}

	if result.Summary.ResourceChanges != nil {
		rc := *result.Summary.ResourceChanges
		log.Printf("[infra] up complete: %d created, %d updated, %d unchanged",
			rc["create"], rc["update"], rc["same"])
	} else {
		log.Printf("[infra] up complete")
	}

	return nil
}

// Down destroys all infrastructure.
func Down(ctx context.Context, cfg *InfraConfig, stateDir string) error {
	s, err := getOrCreateStack(ctx, cfg, stateDir)
	if err != nil {
		return err
	}

	refreshOrImport(ctx, s, cfg)

	log.Printf("[infra] destroying infrastructure...")
	result, err := s.Destroy(ctx, optdestroy.ProgressStreams(os.Stdout))
	if err != nil {
		return fmt.Errorf("pulumi destroy failed: %w", err)
	}

	if result.Summary.ResourceChanges != nil {
		rc := *result.Summary.ResourceChanges
		log.Printf("[infra] destroy complete: %d deleted", rc["delete"])
	} else {
		log.Printf("[infra] destroy complete")
	}

	// Clean up state dir but keep certs
	stateFiles, _ := filepath.Glob(filepath.Join(stateDir, "*"))
	for _, f := range stateFiles {
		os.RemoveAll(f)
	}

	return nil
}

// Preview shows what would change without applying.
func Preview(ctx context.Context, cfg *InfraConfig, stateDir string) error {
	s, err := getOrCreateStack(ctx, cfg, stateDir)
	if err != nil {
		return err
	}

	refreshOrImport(ctx, s, cfg)

	log.Printf("[infra] previewing changes...")
	result, err := s.Preview(ctx)
	if err != nil {
		return fmt.Errorf("pulumi preview failed: %w", err)
	}

	log.Printf("[infra] preview: %d to create, %d to update, %d to delete, %d unchanged",
		result.ChangeSummary["create"],
		result.ChangeSummary["update"],
		result.ChangeSummary["delete"],
		result.ChangeSummary["same"])

	return nil
}
