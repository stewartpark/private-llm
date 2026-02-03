# ──────────────────────────────────────────────────────────────
# Service Accounts
# ──────────────────────────────────────────────────────────────
#
# Each component has its own service account for least-privilege:
# - private-llm-vm: VM instance (compute, storage, secrets)
# - private-llm-proxy: Proxy function (compute, secrets)
# - private-llm-idle-monitoring: Idle monitoring function (compute, storage)
# - private-llm-secret-rotation: Secret rotation (secrets, compute, run)
#
# ──────────────────────────────────────────────────────────────

# VM service account
resource "google_service_account" "vm" {
  account_id   = "private-llm-vm"
  display_name = "Private LLM VM"
  depends_on   = [google_project_service.apis]
}

# Proxy function service account
resource "google_service_account" "proxy" {
  account_id   = "private-llm-proxy"
  display_name = "Private LLM Proxy Function"
  depends_on   = [google_project_service.apis]
}

# Idle monitoring function service account
resource "google_service_account" "idle_monitoring" {
  account_id   = "private-llm-idle-monitor"
  display_name = "Private LLM Idle Monitoring Function"
  depends_on   = [google_project_service.apis]
}

# Secret rotation function service account
resource "google_service_account" "secret_rotation" {
  account_id   = "private-llm-rotation"
  display_name = "Private LLM Secret Rotation"
  depends_on   = [google_project_service.apis]
}

# ──────────────────────────────────────────────────────────────
# Custom Roles
# ──────────────────────────────────────────────────────────────

# Custom role for VM management (least privilege)
resource "google_project_iam_custom_role" "vm_operator" {
  role_id     = "privateLlmVmOperator"
  title       = "Private LLM VM Operator"
  description = "Minimal permissions to get, start, and stop the Private LLM VM"
  permissions = [
    "compute.instances.get",
    "compute.instances.start",
    "compute.instances.stop",
    "compute.zones.get",
  ]
}

# ──────────────────────────────────────────────────────────────
# VM Service Account Permissions
# ──────────────────────────────────────────────────────────────

resource "google_project_iam_member" "vm_logging" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.vm.email}"
}

resource "google_project_iam_member" "vm_monitoring" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.vm.email}"
}

# ──────────────────────────────────────────────────────────────
# Proxy Function Service Account Permissions
# ──────────────────────────────────────────────────────────────

# Custom role: Start VM
resource "google_project_iam_member" "proxy_compute" {
  project = var.project_id
  role    = google_project_iam_custom_role.vm_operator.id
  member  = "serviceAccount:${google_service_account.proxy.email}"
}

# ──────────────────────────────────────────────────────────────
# Idle Monitoring Function Service Account Permissions
# ──────────────────────────────────────────────────────────────

# Custom role: Stop VM
resource "google_project_iam_member" "idle_monitoring_compute" {
  project = var.project_id
  role    = google_project_iam_custom_role.vm_operator.id
  member  = "serviceAccount:${google_service_account.idle_monitoring.email}"
}

# ──────────────────────────────────────────────────────────────
# Secret Rotation Function Service Account Permissions
# ──────────────────────────────────────────────────────────────

# IAM: Compute viewer (check VM status)
resource "google_project_iam_member" "secret_rotation_compute" {
  project = var.project_id
  role    = "roles/compute.viewer"
  member  = "serviceAccount:${google_service_account.secret_rotation.email}"
}

# IAM: Cloud Run developer (redeploy function)
resource "google_project_iam_member" "secret_rotation_run" {
  project = var.project_id
  role    = "roles/run.developer"
  member  = "serviceAccount:${google_service_account.secret_rotation.email}"
}

# Allow rotation function to act as proxy SA (needed to redeploy proxy function)
resource "google_service_account_iam_member" "rotation_act_as_proxy" {
  service_account_id = google_service_account.proxy.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.secret_rotation.email}"
}
