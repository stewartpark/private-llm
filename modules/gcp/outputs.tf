# ──────────────────────────────────────────────────────────────
# GCP Module Outputs
# ──────────────────────────────────────────────────────────────

output "vm_name" {
  value       = google_compute_instance.inference.name
  description = "Name of the inference VM"
}

output "vm_internal_ip" {
  value       = google_compute_instance.inference.network_interface[0].network_ip
  description = "Internal IP of the inference VM"
}

output "secret_rotation_topic" {
  value       = google_pubsub_topic.secret_rotation.name
  description = "Pub/Sub topic for secret rotation"
}

output "kms_key_ring" {
  value       = google_kms_key_ring.main.id
  description = "KMS key ring ID"
}

output "kms_key" {
  value       = google_kms_crypto_key.main.id
  description = "KMS crypto key ID"
}

output "network_name" {
  value       = google_compute_network.main.name
  description = "VPC network name"
}
