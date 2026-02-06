# ──────────────────────────────────────────────────────────────
# Storage Configuration
# ──────────────────────────────────────────────────────────────

# System bucket for Cloud Functions source code
resource "google_storage_bucket" "system" {
  name                        = local.system_bucket_name
  location                    = var.region
  uniform_bucket_level_access = true
  encryption {
    default_kms_key_name = google_kms_crypto_key.main.id
  }
  depends_on = [
    google_project_service.apis,
    google_kms_crypto_key_iam_member.gcs
  ]
}

data "archive_file" "function_zip" {
  type        = "zip"
  source_dir  = "${path.module}/../../function"
  output_path = "${path.module}/../../.terraform/private-llm-function.zip"
}

resource "google_storage_bucket_object" "function_source" {
  name   = "function-${data.archive_file.function_zip.output_md5}.zip"
  bucket = google_storage_bucket.system.name
  source = data.archive_file.function_zip.output_path
}

# ──────────────────────────────────────────────────────────────
# Firestore Database
# ──────────────────────────────────────────────────────────────

# Firestore Native database for VM state tracking
# Uses a named database to avoid conflicts with default database
resource "google_firestore_database" "database" {
  project     = var.project_id
  name        = local.firestore_database_name
  location_id = var.region
  type        = "FIRESTORE_NATIVE"

  depends_on = [google_project_service.apis]
}

# Grant idle monitoring function Firestore read access
resource "google_project_iam_member" "idle_monitoring_firestore" {
  project = var.project_id
  role    = "roles/datastore.user"
  member  = "serviceAccount:${google_service_account.idle_monitoring.email}"

  depends_on = [google_firestore_database.database]
}

# Grant VM service account Firestore write access
resource "google_project_iam_member" "vm_firestore" {
  project = var.project_id
  role    = "roles/datastore.user"
  member  = "serviceAccount:${google_service_account.vm.email}"

  depends_on = [google_firestore_database.database]
}
