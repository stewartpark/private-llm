# ──────────────────────────────────────────────────────────────
# Root Variables
# ──────────────────────────────────────────────────────────────
#
# Variables passed to cloud provider modules.
# GCP-specific variables are passed through to the GCP module.
#
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
  default     = "a2-highgpu-1g"
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

variable "default_model" {
  description = "Default model to pull on first boot"
  type        = string
  default     = "glm-4.7-flash"
}

variable "context_length" {
  description = "Default context length for inference"
  type        = number
  default     = 202752
}

variable "enable_external_ip" {
  description = <<-EOT
    Enable external IP for VM internet access.

    Security modes:
    - true (default): VM can pull models and updates. Already secured by firewall (no inbound access).
                      Use for: initial setup, ongoing maintenance, flexibility to pull new models.
    - false (locked): VM fully isolated from internet. Maximum security, zero supply chain attack surface.
                      Use for: production after initial setup, when model/binaries are frozen.

    Workflow for locked mode:
    1. Deploy with enable_external_ip = true
    2. Wait for initial setup (model downloaded)
    3. Set enable_external_ip = false and terraform apply
    4. VM now fully isolated
    5. To update: temporarily set true, update, set back to false

    Cost impact: External IP = $0 when stopped, ~$0.005/hour when running (~$0.86/month for 40hr/week)
  EOT
  type        = bool
  default     = true
}
