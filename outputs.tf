# ──────────────────────────────────────────────────────────────
# Root Outputs
# ──────────────────────────────────────────────────────────────
#
# Aggregates outputs from the cloud provider module.
#
# ──────────────────────────────────────────────────────────────

output "function_url" {
  value       = module.gcp.function_url
  description = "URL of the Cloud Function proxy"
}

output "vm_name" {
  value       = module.gcp.vm_name
  description = "Name of the inference VM"
}

output "vm_internal_ip" {
  value       = module.gcp.vm_internal_ip
  description = "Internal IP of the inference VM"
}

output "project_id" {
  value       = var.project_id
  description = "GCP project ID"
}

output "zone" {
  value       = var.zone
  description = "GCP zone for the VM"
}

output "secret_rotation_topic" {
  value       = module.gcp.secret_rotation_topic
  description = "Pub/Sub topic for secret rotation (no HTTP endpoint)"
}

output "kms_key_ring" {
  value       = module.gcp.kms_key_ring
  description = "KMS key ring for infrastructure"
}

output "kms_key" {
  value       = module.gcp.kms_key
  description = "KMS crypto key (all resources)"
}

output "api_token" {
  value       = local.bootstrap_api_token
  description = "API token for authentication (bootstrap value, may be rotated in Secret Manager)"
  sensitive   = true
}

output "usage" {
  value = <<-EOT

    # Get your API token
    terraform output -raw api_token

    # Pull a model (starts VM if stopped, ~60s warm start, ~6-11min first boot)
    curl -H "Authorization: Bearer <API_TOKEN>" ${module.gcp.function_url}/api/pull -d '{"name":"llama3.2:1b"}'

    # List models
    curl -H "Authorization: Bearer <API_TOKEN>" ${module.gcp.function_url}/api/tags

    # Chat
    curl -H "Authorization: Bearer <API_TOKEN>" ${module.gcp.function_url}/api/chat -d '{"model":"llama3.2:1b","messages":[{"role":"user","content":"Hello"}]}'

    # First boot: Installation takes 6-11 minutes. Proxy returns 503 with progress during install.
  EOT
}
