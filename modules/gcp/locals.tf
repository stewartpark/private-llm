# ──────────────────────────────────────────────────────────────
# GCP Module Locals
# ──────────────────────────────────────────────────────────────

locals {
  # System bucket for Cloud Functions source code
  system_bucket_name = "${var.project_id}-private-llm-system"

  # Function names (defined here to avoid circular dependencies)
  function_secret_rotation_name = "private-llm-secret-rotation"

  # Firestore database name
  firestore_database_name = "private-llm"
}
