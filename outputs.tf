# ──────────────────────────────────────────────────────────────
# Root Outputs
# ──────────────────────────────────────────────────────────────
#
# Aggregates outputs from the cloud provider module.
#
# ──────────────────────────────────────────────────────────────

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

output "network" {
  value       = module.gcp.network_name
  description = "VPC network name"
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

output "usage" {
  value = <<-EOT

    # Install the local agent
    make install

    # Run the agent (listens on localhost:11434, acts as Ollama)
    private-llm-agent

    # In another terminal:
    ollama ls              # List models
    ollama pull llama3.2   # Pull a model
    ollama run llama3.2    # Chat with a model

    # Ctrl+C the agent to clean up firewall rule
    # VM auto-stops after ${var.idle_timeout}s idle
  EOT
}
