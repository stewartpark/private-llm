.PHONY: deploy install test clean rotate-mtls rotate-mtls-ca

# Deploy infrastructure
deploy:
ifndef TFSTATE_BUCKET
	$(error TFSTATE_BUCKET is not set. Run: TFSTATE_BUCKET=your-bucket-name make deploy)
endif
	@echo "Deploying infrastructure..."
	@echo "Using tfstate bucket: $(TFSTATE_BUCKET)"
	terraform init -backend-config="bucket=$(TFSTATE_BUCKET)"
	terraform apply -auto-approve
	@echo ""
	@echo "Deployment complete!"
	@echo "Run 'make install' to build the local agent"

# Build agent binary, install to PATH, and generate config
install:
	@echo "Building agent..."
	cd agent && go build -o ../bin/private-llm-agent .
	@echo "Installing to ~/.local/bin..."
	@mkdir -p ~/.local/bin
	cp bin/private-llm-agent ~/.local/bin/private-llm-agent
	@echo "Generating agent config..."
	@mkdir -p ~/.config/private-llm
	@terraform output -json | jq '{ \
		project_id: .project_id.value, \
		zone: .zone.value, \
		vm_name: .vm_name.value, \
		network: .network.value \
	}' > ~/.config/private-llm/agent.json
	@echo "Config written to ~/.config/private-llm/agent.json"
	@echo ""
	@echo "Run: private-llm-agent"

test:
	@echo "Testing LLM deployment..."
	@TEST_MODEL="$${MODEL:-glm-4.7-flash}"; \
	echo "Using endpoint: http://localhost:11434"; \
	echo "Model: $$TEST_MODEL"; \
	echo ""; \
	echo "=== Pulling $$TEST_MODEL ==="; \
	curl -s -X POST \
		-H "Content-Type: application/json" \
		-d "{\"name\":\"$$TEST_MODEL\"}" \
		"http://localhost:11434/api/pull" | while read -r line; do \
			echo "$$line"; \
		done; \
	echo ""; \
	echo "=== Running generation ==="; \
	RESPONSE=$$(curl -s -X POST \
		-H "Content-Type: application/json" \
		-d "{\"model\":\"$$TEST_MODEL\",\"prompt\":\"Hello, how are you?\",\"stream\":false}" \
		"http://localhost:11434/api/generate"); \
	echo "$$RESPONSE" | jq . 2>/dev/null || echo "$$RESPONSE"; \
	echo ""; \
	echo "=== Done ==="

clean:
	@echo "Warning: This will destroy all infrastructure!"
	@read -p "Are you sure? [y/N] " -n 1 -r; \
	echo; \
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then \
		terraform destroy -auto-approve; \
	fi

# Rotate mTLS certificates (server + client, keeps existing CA)
rotate-mtls:
	@echo "Rotating mTLS certificates (server + client)..."
	@PROJECT_ID=$$(terraform output -raw project_id 2>/dev/null || gcloud config get-value project); \
	echo "Project: $$PROJECT_ID"; \
	echo "Publishing rotation request..."; \
	gcloud pubsub topics publish private-llm-secret-rotation \
		--project=$$PROJECT_ID \
		--message='{"force":true}' && \
	echo "Rotation triggered"; \
	echo ""; \
	echo "Monitor logs:"; \
	echo "  gcloud functions logs read private-llm-secret-rotation --project=$$PROJECT_ID --limit=50"

# Rotate CA + all mTLS certificates (full regeneration)
rotate-mtls-ca:
	@echo "WARNING: This will regenerate the CA and ALL certificates"
	@echo "   VM will need to restart to pick up new certs"
	@echo ""
	@read -p "Continue? [y/N] " -n 1 -r; \
	echo; \
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then \
		PROJECT_ID=$$(terraform output -raw project_id 2>/dev/null || gcloud config get-value project); \
		ZONE=$$(terraform output -raw zone 2>/dev/null || echo "us-central1-a"); \
		VM_NAME=$$(terraform output -raw vm_name 2>/dev/null || echo "private-llm-vm"); \
		echo "Project: $$PROJECT_ID"; \
		echo "Publishing CA rotation request..."; \
		gcloud pubsub topics publish private-llm-secret-rotation \
			--project=$$PROJECT_ID \
			--message='{"rotate_ca":true,"force":true}' && \
		echo "CA rotation triggered"; \
		echo ""; \
		echo "Stopping VM to load new certificates..."; \
		gcloud compute instances stop $$VM_NAME --zone=$$ZONE --project=$$PROJECT_ID --quiet && \
		echo "VM stopped (will load new certs on next start)"; \
		echo ""; \
		echo "Monitor logs:"; \
		echo "  gcloud functions logs read private-llm-secret-rotation --project=$$PROJECT_ID --limit=50"; \
	fi
