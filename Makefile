.PHONY: install deploy test clean key rotate-key rotate-mtls rotate-mtls-ca pull-model list-models

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
	@echo "Run 'make install' to configure OpenCode"

# Generate and install OpenCode provider to user scope
install:
	@echo "Generating OpenCode configuration..."
	@PROXY_URL=$$(terraform output -raw function_url); \
	API_TOKEN=$$(gcloud secrets versions access latest --secret=private-llm-api-token 2>/dev/null || echo ""); \
	if [ -z "$$API_TOKEN" ]; then \
		echo "No API token found"; \
		exit 1; \
	fi; \
	sed -e "s#\$${PROXY_URL}#$$PROXY_URL#g" \
	    -e "s#\$${API_TOKEN}#$$API_TOKEN#g" \
	    config/opencode.json.tmpl > opencode.json
	@echo "✓ opencode.json generated"
	@echo ""
	@echo "Merging provider to user config..."
	@mkdir -p ~/.config/opencode
	@if [ -f ~/.config/opencode/config.json ]; then \
		jq '.provider["private-llm"] = input.provider["private-llm"]' \
			~/.config/opencode/config.json opencode.json > ~/.config/opencode/config.json.tmp && \
		mv ~/.config/opencode/config.json.tmp ~/.config/opencode/config.json; \
	else \
		jq '{provider: .provider}' opencode.json > ~/.config/opencode/config.json; \
	fi
	@echo "✓ Provider merged"

test:
	@echo "Testing LLM deployment..."
	@API_TOKEN=$$(gcloud secrets versions access latest --secret=private-llm-api-token 2>/dev/null); \
	if [ -z "$$API_TOKEN" ]; then \
		echo "No API token found"; \
		exit 1; \
	fi; \
	FUNCTION_URL=$$(terraform output -raw function_url 2>/dev/null); \
	TEST_MODEL="$${MODEL:-glm-4.7-flash}"; \
	echo "Using endpoint: $$FUNCTION_URL"; \
	echo "API token: $${API_TOKEN:0:8}..."; \
	echo "Model: $$TEST_MODEL"; \
	echo ""; \
	echo "=== Pulling $$TEST_MODEL ==="; \
	curl -s -X POST \
		-H "Authorization: Bearer $$API_TOKEN" \
		-H "Content-Type: application/json" \
		-d "{\"name\":\"$$TEST_MODEL\"}" \
		"$$FUNCTION_URL/api/pull" | while read -r line; do \
			echo "$$line"; \
		done; \
	echo ""; \
	echo "=== Running generation ==="; \
	RESPONSE=$$(curl -s -X POST \
		-H "Authorization: Bearer $$API_TOKEN" \
		-H "Content-Type: application/json" \
		-d "{\"model\":\"$$TEST_MODEL\",\"prompt\":\"Hello, how are you?\",\"stream\":false}" \
		"$$FUNCTION_URL/api/generate"); \
	echo "$$RESPONSE" | jq . 2>/dev/null || echo "$$RESPONSE"; \
	echo ""; \
	echo "=== Done ==="

# Pull a specific model
pull-model:
	@if [ -z "$(MODEL)" ]; then \
		echo "Error: MODEL variable not set"; \
		echo "Usage: MODEL=llama3.2:3b make pull-model"; \
		exit 1; \
	fi; \
	API_TOKEN=$$(gcloud secrets versions access latest --secret=private-llm-api-token 2>/dev/null); \
	if [ -z "$$API_TOKEN" ]; then \
		echo "No API token found"; \
		exit 1; \
	fi; \
	FUNCTION_URL=$$(terraform output -raw function_url 2>/dev/null); \
	echo "Pulling model: $(MODEL)"; \
	echo "Endpoint: $$FUNCTION_URL"; \
	curl -X POST \
		-H "Authorization: Bearer $$API_TOKEN" \
		-H "Content-Type: application/json" \
		-d '{"name":"$(MODEL)"}' \
		"$$FUNCTION_URL/api/pull"

# List all available models
list-models:
	@API_TOKEN=$$(gcloud secrets versions access latest --secret=private-llm-api-token 2>/dev/null); \
	if [ -z "$$API_TOKEN" ]; then \
		echo "No API token found"; \
		exit 1; \
	fi; \
	FUNCTION_URL=$$(terraform output -raw function_url 2>/dev/null); \
	echo "Listing models from: $$FUNCTION_URL"; \
	echo ""; \
	curl -s -X GET \
		-H "Authorization: Bearer $$API_TOKEN" \
		"$$FUNCTION_URL/api/tags" | jq -r '.models[] | "  \(.name) (\(.size / 1024 / 1024 / 1024 | floor)GB)"' 2>/dev/null || \
		curl -s -X GET \
			-H "Authorization: Bearer $$API_TOKEN" \
			"$$FUNCTION_URL/api/tags"

clean:
	@echo "Warning: This will destroy all infrastructure!"
	@read -p "Are you sure? [y/N] " -n 1 -r; \
	echo; \
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then \
		terraform destroy -auto-approve; \
	fi

# Get external API token (generates one if none exists)
key:
	@gcloud secrets versions access latest --secret=private-llm-api-token 2>/dev/null || $(MAKE) rotate-key

# Generate new external API token (90-day TTL enforced by Secret Manager)
rotate-key:
	@openssl rand -hex 32 | gcloud secrets versions add private-llm-api-token --data-file=- && \
	gcloud secrets versions access latest --secret=private-llm-api-token

# Rotate mTLS certificates (server + client, keeps existing CA)
rotate-mtls:
	@echo "Rotating mTLS certificates (server + client)..."
	@PROJECT_ID=$$(terraform output -raw project_id 2>/dev/null || gcloud config get-value project); \
	echo "Project: $$PROJECT_ID"; \
	echo "Publishing rotation request..."; \
	gcloud pubsub topics publish private-llm-secret-rotation \
		--project=$$PROJECT_ID \
		--message='{"force":true}' && \
	echo "✓ Rotation triggered"; \
	echo ""; \
	echo "Monitor logs:"; \
	echo "  gcloud functions logs read private-llm-secret-rotation --project=$$PROJECT_ID --limit=50"

# Rotate CA + all mTLS certificates (full regeneration)
rotate-mtls-ca:
	@echo "⚠️  WARNING: This will regenerate the CA and ALL certificates"
	@echo "   Both proxy function and VM will need to restart to pick up new certs"
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
		echo "✓ CA rotation triggered"; \
		echo ""; \
		echo "Stopping VM to load new certificates..."; \
		gcloud compute instances stop $$VM_NAME --zone=$$ZONE --project=$$PROJECT_ID --quiet && \
		echo "✓ VM stopped (will load new certs on next start)"; \
		echo ""; \
		echo "Monitor logs:"; \
		echo "  gcloud functions logs read private-llm-secret-rotation --project=$$PROJECT_ID --limit=50"; \
	fi
