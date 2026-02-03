# ──────────────────────────────────────────────────────────────
# Cloud Functions
# ──────────────────────────────────────────────────────────────

# ──────────────────────────────────────────────────────────────
# Proxy Function
# ──────────────────────────────────────────────────────────────
#
# Public-facing proxy function that:
# 1. Validates external API token
# 2. Starts VM if stopped
# 3. Replaces external token with internal token
# 4. Forwards request to VM via mTLS
#
# ──────────────────────────────────────────────────────────────

resource "google_cloudfunctions2_function" "proxy" {
  name     = local.function_proxy_name
  location = var.region

  build_config {
    runtime     = "go125"
    entry_point = "PrivateLlmProxy"
    source {
      storage_source {
        bucket = google_storage_bucket.system.name
        object = google_storage_bucket_object.function_source.name
      }
    }
  }

  service_config {
    max_instance_count    = 1
    min_instance_count    = 0
    available_memory      = "256Mi"
    timeout_seconds       = 540
    service_account_email = google_service_account.proxy.email
    ingress_settings      = "ALLOW_ALL"

    # Direct VPC egress - no connector needed!
    direct_vpc_network_interface {
      network    = google_compute_network.main.name
      subnetwork = google_compute_subnetwork.main.name
      tags       = ["private-llm-proxy"]
    }
    direct_vpc_egress = "VPC_EGRESS_ALL_TRAFFIC"

    environment_variables = {
      GCP_PROJECT        = var.project_id
      GCP_ZONE           = var.zone
      VM_NAME            = var.vm_name
      IDLE_TIMEOUT       = var.idle_timeout
      FIRESTORE_DATABASE = google_firestore_database.database.name
    }

    secret_environment_variables {
      key        = "CA_CERT"
      project_id = var.project_id
      secret     = google_secret_manager_secret.ca_cert.secret_id
      version    = "latest"
    }

    secret_environment_variables {
      key        = "CLIENT_CERT"
      project_id = var.project_id
      secret     = google_secret_manager_secret.client_cert.secret_id
      version    = "latest"
    }

    secret_environment_variables {
      key        = "CLIENT_KEY"
      project_id = var.project_id
      secret     = google_secret_manager_secret.client_key.secret_id
      version    = "latest"
    }

    secret_environment_variables {
      key        = "API_TOKEN"
      project_id = var.project_id
      secret     = google_secret_manager_secret.api_token.secret_id
      version    = "latest"
    }

    secret_environment_variables {
      key        = "INTERNAL_TOKEN"
      project_id = var.project_id
      secret     = google_secret_manager_secret.internal_token.secret_id
      version    = "latest"
    }
  }

  depends_on = [
    google_project_service.apis,
    # Wait for initial secrets to be generated
    google_secret_manager_secret_version.ca_cert_initial,
    google_secret_manager_secret_version.client_cert_initial,
    google_secret_manager_secret_version.client_key_initial,
    google_secret_manager_secret_version.internal_token_initial,
    google_secret_manager_secret_version.api_token_initial,
  ]
}

# Allow public access to proxy function (external token validates requests)
resource "google_cloud_run_service_iam_member" "public" {
  location = var.region
  service  = google_cloudfunctions2_function.proxy.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# ──────────────────────────────────────────────────────────────
# Idle Monitoring Function
# ──────────────────────────────────────────────────────────────
#
# Background function that monitors last_request timestamp and
# stops VM if idle for longer than configured timeout.
# Triggered every 5 minutes by Cloud Scheduler.
#
# ──────────────────────────────────────────────────────────────

resource "google_pubsub_topic" "idle_monitoring" {
  name       = "private-llm-idle-monitoring"
  depends_on = [google_project_service.apis]
}

resource "google_cloudfunctions2_function" "idle_monitoring" {
  name     = "private-llm-idle-monitoring"
  location = var.region

  build_config {
    runtime     = "go125"
    entry_point = "IdleMonitoring"
    source {
      storage_source {
        bucket = google_storage_bucket.system.name
        object = google_storage_bucket_object.function_source.name
      }
    }
  }

  service_config {
    max_instance_count             = 1
    min_instance_count             = 0
    available_memory               = "256Mi"
    timeout_seconds                = 60
    ingress_settings               = "ALLOW_INTERNAL_ONLY"
    all_traffic_on_latest_revision = true
    service_account_email          = google_service_account.idle_monitoring.email

    # Direct VPC egress for idle monitoring function
    direct_vpc_network_interface {
      network    = google_compute_network.main.name
      subnetwork = google_compute_subnetwork.main.name
      tags       = ["private-llm-idle-monitoring"]
    }
    direct_vpc_egress = "VPC_EGRESS_ALL_TRAFFIC"

    environment_variables = {
      GCP_PROJECT        = var.project_id
      GCP_ZONE           = var.zone
      VM_NAME            = var.vm_name
      IDLE_TIMEOUT       = var.idle_timeout
      FIRESTORE_DATABASE = google_firestore_database.database.name
    }
  }

  event_trigger {
    trigger_region        = var.region
    event_type            = "google.cloud.pubsub.topic.v1.messagePublished"
    pubsub_topic          = google_pubsub_topic.idle_monitoring.id
    retry_policy          = "RETRY_POLICY_DO_NOT_RETRY"
    service_account_email = google_service_account.idle_monitoring.email
  }

  depends_on = [google_project_service.apis]
}

# Allow idle monitoring SA to invoke its own function via Pub/Sub push
resource "google_cloud_run_service_iam_member" "idle_monitoring_invoker" {
  location = var.region
  service  = google_cloudfunctions2_function.idle_monitoring.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.idle_monitoring.email}"
}

resource "google_cloud_scheduler_job" "idle_monitoring" {
  name      = "private-llm-idle-monitoring"
  region    = var.region
  schedule  = "*/5 * * * *"
  time_zone = "UTC"

  pubsub_target {
    topic_name = google_pubsub_topic.idle_monitoring.id
    data       = base64encode("check")
  }

  depends_on = [google_project_service.apis]
}

# ──────────────────────────────────────────────────────────────
# Secret Rotation Function
# ──────────────────────────────────────────────────────────────
#
# Secret rotation orchestrator that:
# 1. Checks if rotation is safe (VM stopped, certs expiring)
# 2. Generates new mTLS certificates and internal token
# 3. Creates new Secret Manager versions
# 4. Redeploys proxy function to pick up new secrets
#
# ──────────────────────────────────────────────────────────────

# Pub/Sub topic for secret rotation
resource "google_pubsub_topic" "secret_rotation" {
  name = "private-llm-secret-rotation"
}

# Secret rotation function (Pub/Sub triggered)
resource "google_cloudfunctions2_function" "secret_rotation" {
  name        = local.function_secret_rotation_name
  location    = var.region
  description = "Auto-rotates mTLS certificates and internal token"

  build_config {
    runtime     = "go125"
    entry_point = "SecretRotation"
    source {
      storage_source {
        bucket = google_storage_bucket.system.name
        object = google_storage_bucket_object.function_source.name
      }
    }
  }

  service_config {
    max_instance_count    = 1
    min_instance_count    = 0
    available_memory      = "256Mi"
    timeout_seconds       = 300 # 5 minutes max
    service_account_email = google_service_account.secret_rotation.email
    ingress_settings      = "ALLOW_INTERNAL_ONLY"

    environment_variables = {
      GCP_PROJECT   = var.project_id
      GCP_ZONE      = var.zone
      GCP_REGION    = var.region
      VM_NAME       = var.vm_name
      FUNCTION_NAME = local.function_proxy_name
    }
  }

  event_trigger {
    trigger_region        = var.region
    event_type            = "google.cloud.pubsub.topic.v1.messagePublished"
    pubsub_topic          = google_pubsub_topic.secret_rotation.id
    retry_policy          = "RETRY_POLICY_DO_NOT_RETRY"
    service_account_email = google_service_account.secret_rotation.email
  }

  depends_on = [google_project_service.apis]
}

# Cloud Scheduler job (every 2 hours rotation check)
resource "google_cloud_scheduler_job" "secret_rotation" {
  name        = "private-llm-secret-rotation"
  schedule    = "0 */2 * * *" # Every 2 hours
  time_zone   = "UTC"
  description = "Every 2 hours check for certificate rotation eligibility (1-week cert validity, triggers at <24h remaining = 12 attempts in final day)"

  pubsub_target {
    topic_name = google_pubsub_topic.secret_rotation.id
    data       = base64encode(jsonencode({ auto = true }))
  }

  depends_on = [google_project_service.apis]
}

# Allow scheduler to trigger rotation function via Pub/Sub
resource "google_pubsub_topic_iam_member" "secret_rotation_scheduler_publisher" {
  topic  = google_pubsub_topic.secret_rotation.name
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:service-${data.google_project.project.number}@gcp-sa-cloudscheduler.iam.gserviceaccount.com"
}

# Allow secret rotation SA to invoke its own function via Pub/Sub push
resource "google_cloud_run_service_iam_member" "secret_rotation_invoker" {
  location = var.region
  service  = google_cloudfunctions2_function.secret_rotation.name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.secret_rotation.email}"
}
