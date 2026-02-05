# ──────────────────────────────────────────────────────────────
# GCP Module Variables
# ──────────────────────────────────────────────────────────────

# ──────────────────────────────────────────────────────────────
# GCP Configuration
# ──────────────────────────────────────────────────────────────

variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "GCP zone"
  type        = string
  default     = "us-central1-a"
}

# ──────────────────────────────────────────────────────────────
# Bootstrap Secrets (from root)
# ──────────────────────────────────────────────────────────────

variable "bootstrap_ca_cert_pem" {
  description = "Bootstrap CA certificate PEM (overwritten by rotation function)"
  type        = string
  sensitive   = true
}

variable "bootstrap_ca_key_pem" {
  description = "Bootstrap CA private key PEM (overwritten by rotation function)"
  type        = string
  sensitive   = true
}

variable "bootstrap_server_cert_pem" {
  description = "Bootstrap server certificate PEM (overwritten by rotation function)"
  type        = string
  sensitive   = true
}

variable "bootstrap_server_key_pem" {
  description = "Bootstrap server private key PEM (overwritten by rotation function)"
  type        = string
  sensitive   = true
}

variable "bootstrap_client_cert_pem" {
  description = "Bootstrap client certificate PEM (overwritten by rotation function)"
  type        = string
  sensitive   = true
}

variable "bootstrap_client_key_pem" {
  description = "Bootstrap client private key PEM (overwritten by rotation function)"
  type        = string
  sensitive   = true
}

variable "bootstrap_internal_token" {
  description = "Bootstrap internal token (overwritten by rotation function)"
  type        = string
  sensitive   = true
}

variable "bootstrap_api_token" {
  description = "Bootstrap API token (user-managed, persists)"
  type        = string
  sensitive   = true
}

# ──────────────────────────────────────────────────────────────
# VM Configuration
# ──────────────────────────────────────────────────────────────

variable "vm_name" {
  description = "Name of the inference VM"
  type        = string
  default     = "private-llm-vm"
}

variable "machine_type" {
  description = "Machine type for the VM"
  type        = string
  default     = "g4-standard-48"
}

variable "default_model" {
  description = "Default model to pull on first boot"
  type        = string
  default     = "qwen3-coder-next:q8_0"
}

variable "context_length" {
  description = "Default context length for inference"
  type        = number
  default     = 262144
}

variable "idle_timeout" {
  description = "Idle timeout in seconds for VM"
  type        = number
  default     = 300
}

variable "subnet_cidr" {
  description = "CIDR block for the VPC subnet"
  type        = string
  default     = "10.10.0.0/24"
}

variable "enable_external_ip" {
  description = "Enable external IP for VM internet access"
  type        = bool
  default     = true
}
