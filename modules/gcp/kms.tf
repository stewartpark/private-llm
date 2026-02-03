# ──────────────────────────────────────────────────────────────
# KMS Configuration
# ──────────────────────────────────────────────────────────────

# Explicitly create Secret Manager service account
resource "google_project_service_identity" "secretmanager" {
  project = var.project_id
  service = "secretmanager.googleapis.com"

  depends_on = [google_project_service.apis]
}

# KMS Key Ring (regional)
resource "google_kms_key_ring" "main" {
  name     = "private-llm-keyring"
  location = var.region
}

# KMS Key: Single key for all resources
resource "google_kms_crypto_key" "main" {
  name            = "private-llm-key"
  key_ring        = google_kms_key_ring.main.id
  rotation_period = "7776000s" # 90 days
  purpose         = "ENCRYPT_DECRYPT"

  version_template {
    algorithm        = "GOOGLE_SYMMETRIC_ENCRYPTION"
    protection_level = "HSM"
  }

  lifecycle {
    prevent_destroy = false
  }
}

# IAM: Secret Manager service agent → key
resource "google_kms_crypto_key_iam_member" "secretmanager" {
  crypto_key_id = google_kms_crypto_key.main.id
  role          = "roles/cloudkms.cryptoKeyEncrypterDecrypter"
  member        = "serviceAccount:service-${data.google_project.project.number}@gcp-sa-secretmanager.iam.gserviceaccount.com"

  depends_on = [google_project_service_identity.secretmanager]
}

# IAM: GCS service agent → key (for function source bucket only)
resource "google_kms_crypto_key_iam_member" "gcs" {
  crypto_key_id = google_kms_crypto_key.main.id
  role          = "roles/cloudkms.cryptoKeyEncrypterDecrypter"
  member        = "serviceAccount:service-${data.google_project.project.number}@gs-project-accounts.iam.gserviceaccount.com"
}
