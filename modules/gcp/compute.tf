# ──────────────────────────────────────────────────────────────
# Compute (VM)
# ──────────────────────────────────────────────────────────────
#
# Single inference VM with GPU, Spot provisioning, cloud-init installation.
# First boot runs cloud-init script (6-11 minutes one-time setup).
# Subsequent boots take ~45-60 seconds.
#
# ──────────────────────────────────────────────────────────────

# Inference VM: GPU-enabled with cloud-init installation
resource "google_compute_instance" "inference" {
  name                      = var.vm_name
  machine_type              = var.machine_type
  zone                      = var.zone
  allow_stopping_for_update = true

  scheduling {
    provisioning_model          = "SPOT"
    preemptible                 = true
    automatic_restart           = false
    instance_termination_action = "STOP"
    on_host_maintenance         = "TERMINATE"
  }

  boot_disk {
    initialize_params {
      # Use base Deep Learning VM image (Ubuntu 24.04, CUDA 12.8, NVIDIA drivers)
      image = "projects/deeplearning-platform-release/global/images/family/common-cu128-ubuntu-2404-nvidia-570"
      size  = 128
      type  = "hyperdisk-balanced"
      # ~600 MB/s provisioned = 50GB model loads in ~83 seconds
      # IOPS: minimum 3000 sufficient for large file workloads (sequential reads)
      # Cost: ~$9.98 capacity + $0 IOPS + ~$8.40 throughput = ~$18/month
      provisioned_iops       = 3000
      provisioned_throughput = 700
    }
  }

  shielded_instance_config {
    enable_secure_boot          = true # NVIDIA official drivers are signed
    enable_vtpm                 = true
    enable_integrity_monitoring = true
  }

  network_interface {
    network    = google_compute_network.main.name
    subnetwork = google_compute_subnetwork.main.name

    # Conditionally enable external IP based on security posture
    # When enabled: VM can pull models/updates (egress only, firewall blocks inbound)
    # When disabled: VM fully isolated from internet (maximum security)
    dynamic "access_config" {
      for_each = var.enable_external_ip ? [1] : []
      content {}
    }
  }

  tags = ["private-llm"]

  service_account {
    email = google_service_account.vm.email
    scopes = [
      "https://www.googleapis.com/auth/monitoring.write",
      "https://www.googleapis.com/auth/cloud-platform", # Needed for Firestore write access
    ]
  }

  metadata = {
    caddyfile               = file("${path.module}/../../config/Caddyfile")
    context-length          = var.context_length
    model                   = var.default_model
    enable-osconfig         = "TRUE"
    enable-guest-attributes = "TRUE"
    # Startup script runs on every boot, but script checks for completion marker
    startup-script = file("${path.module}/../../config/vm-startup.sh")
  }

  depends_on = [
    google_project_service.apis,
    google_firestore_database.database,
    # Wait for initial mTLS secrets to be generated
    google_secret_manager_secret_version.ca_cert_initial,
    google_secret_manager_secret_version.server_cert_initial,
    google_secret_manager_secret_version.server_key_initial,
    google_secret_manager_secret_version.internal_token_initial,
  ]
}
