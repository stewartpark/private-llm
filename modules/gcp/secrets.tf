# ──────────────────────────────────────────────────────────────
# Secret Manager Secrets
# ──────────────────────────────────────────────────────────────
#
# Stores bootstrap secrets in Secret Manager with KMS encryption.
# Initial values come from root module's bootstrap.tf.
# The rotation function can rotate these later.
#
# ──────────────────────────────────────────────────────────────

# ──────────────────────────────────────────────────────────────
# mTLS Secret Definitions
# ──────────────────────────────────────────────────────────────

# CA Certificate Secret
resource "google_secret_manager_secret" "ca_cert" {
  secret_id = "private-llm-ca-cert"
  replication {
    user_managed {
      replicas {
        location = var.region
        customer_managed_encryption {
          kms_key_name = google_kms_crypto_key.main.id
        }
      }
    }
  }
  # Automatic version cleanup: keep 3 versions, auto-delete after 30 days
  version_destroy_ttl = "2592000s" # 30 days
  depends_on = [
    google_project_service.apis,
    google_kms_crypto_key_iam_member.secretmanager
  ]
}

# CA Key Secret
resource "google_secret_manager_secret" "ca_key" {
  secret_id = "private-llm-ca-key"
  replication {
    user_managed {
      replicas {
        location = var.region
        customer_managed_encryption {
          kms_key_name = google_kms_crypto_key.main.id
        }
      }
    }
  }
  version_destroy_ttl = "2592000s" # 30 days
  depends_on = [
    google_project_service.apis,
    google_kms_crypto_key_iam_member.secretmanager
  ]
}

# Server Certificate Secret
resource "google_secret_manager_secret" "server_cert" {
  secret_id = "private-llm-server-cert"
  replication {
    user_managed {
      replicas {
        location = var.region
        customer_managed_encryption {
          kms_key_name = google_kms_crypto_key.main.id
        }
      }
    }
  }
  version_destroy_ttl = "2592000s" # 30 days
  depends_on = [
    google_project_service.apis,
    google_kms_crypto_key_iam_member.secretmanager
  ]
}

# Server Key Secret
resource "google_secret_manager_secret" "server_key" {
  secret_id = "private-llm-server-key"
  replication {
    user_managed {
      replicas {
        location = var.region
        customer_managed_encryption {
          kms_key_name = google_kms_crypto_key.main.id
        }
      }
    }
  }
  version_destroy_ttl = "2592000s" # 30 days
  depends_on = [
    google_project_service.apis,
    google_kms_crypto_key_iam_member.secretmanager
  ]
}

# Client Certificate Secret
resource "google_secret_manager_secret" "client_cert" {
  secret_id = "private-llm-client-cert"
  replication {
    user_managed {
      replicas {
        location = var.region
        customer_managed_encryption {
          kms_key_name = google_kms_crypto_key.main.id
        }
      }
    }
  }
  version_destroy_ttl = "2592000s" # 30 days
  depends_on = [
    google_project_service.apis,
    google_kms_crypto_key_iam_member.secretmanager
  ]
}

# Client Key Secret
resource "google_secret_manager_secret" "client_key" {
  secret_id = "private-llm-client-key"
  replication {
    user_managed {
      replicas {
        location = var.region
        customer_managed_encryption {
          kms_key_name = google_kms_crypto_key.main.id
        }
      }
    }
  }
  version_destroy_ttl = "2592000s" # 30 days
  depends_on = [
    google_project_service.apis,
    google_kms_crypto_key_iam_member.secretmanager
  ]
}

# ──────────────────────────────────────────────────────────────
# Token Secret Definitions
# ──────────────────────────────────────────────────────────────

# External API Token (user → function authentication)
# User-managed via `make rotate-key`
# Secret Manager enforces 90-day TTL (auto-expires)
resource "google_secret_manager_secret" "api_token" {
  secret_id = "private-llm-api-token"
  replication {
    user_managed {
      replicas {
        location = var.region
        customer_managed_encryption {
          kms_key_name = google_kms_crypto_key.main.id
        }
      }
    }
  }
  # Token versions auto-expire after 90 days
  ttl = "7776000s" # 90 days
  depends_on = [
    google_project_service.apis,
    google_kms_crypto_key_iam_member.secretmanager
  ]
}

# Internal Token (function → VM authentication)
# Auto-rotated by secret rotation function
resource "google_secret_manager_secret" "internal_token" {
  secret_id = "private-llm-internal-token"
  replication {
    user_managed {
      replicas {
        location = var.region
        customer_managed_encryption {
          kms_key_name = google_kms_crypto_key.main.id
        }
      }
    }
  }
  version_destroy_ttl = "2592000s" # 30 days
  depends_on = [
    google_project_service.apis,
    google_kms_crypto_key_iam_member.secretmanager
  ]
}

# ──────────────────────────────────────────────────────────────
# Initial Secret Versions (from bootstrap)
# ──────────────────────────────────────────────────────────────

# CA Certificate
resource "google_secret_manager_secret_version" "ca_cert_initial" {
  secret      = google_secret_manager_secret.ca_cert.id
  secret_data = var.bootstrap_ca_cert_pem
}

# CA Private Key
resource "google_secret_manager_secret_version" "ca_key_initial" {
  secret      = google_secret_manager_secret.ca_key.id
  secret_data = var.bootstrap_ca_key_pem
}

# Server Certificate
resource "google_secret_manager_secret_version" "server_cert_initial" {
  secret      = google_secret_manager_secret.server_cert.id
  secret_data = var.bootstrap_server_cert_pem
}

# Server Private Key
resource "google_secret_manager_secret_version" "server_key_initial" {
  secret      = google_secret_manager_secret.server_key.id
  secret_data = var.bootstrap_server_key_pem
}

# Client Certificate
resource "google_secret_manager_secret_version" "client_cert_initial" {
  secret      = google_secret_manager_secret.client_cert.id
  secret_data = var.bootstrap_client_cert_pem
}

# Client Private Key
resource "google_secret_manager_secret_version" "client_key_initial" {
  secret      = google_secret_manager_secret.client_key.id
  secret_data = var.bootstrap_client_key_pem
}

# API Token (user should rotate via `make rotate-key`)
resource "google_secret_manager_secret_version" "api_token_initial" {
  secret      = google_secret_manager_secret.api_token.id
  secret_data = var.bootstrap_api_token

  lifecycle {
    ignore_changes = [secret_data] # Don't update when user rotates manually
  }
}

# Internal Token
resource "google_secret_manager_secret_version" "internal_token_initial" {
  secret      = google_secret_manager_secret.internal_token.id
  secret_data = var.bootstrap_internal_token
}

# ──────────────────────────────────────────────────────────────
# Secret IAM Bindings
# ──────────────────────────────────────────────────────────────

# VM access to secrets (mTLS + internal token)
resource "google_secret_manager_secret_iam_member" "vm_ca_cert" {
  secret_id = google_secret_manager_secret.ca_cert.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.vm.email}"
}

resource "google_secret_manager_secret_iam_member" "vm_server_cert" {
  secret_id = google_secret_manager_secret.server_cert.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.vm.email}"
}

resource "google_secret_manager_secret_iam_member" "vm_server_key" {
  secret_id = google_secret_manager_secret.server_key.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.vm.email}"
}

resource "google_secret_manager_secret_iam_member" "vm_internal_token" {
  secret_id = google_secret_manager_secret.internal_token.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.vm.email}"
}

# Proxy function access to secrets (mTLS + both tokens)
resource "google_secret_manager_secret_iam_member" "proxy_ca_cert" {
  secret_id = google_secret_manager_secret.ca_cert.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.proxy.email}"
}

resource "google_secret_manager_secret_iam_member" "proxy_client_cert" {
  secret_id = google_secret_manager_secret.client_cert.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.proxy.email}"
}

resource "google_secret_manager_secret_iam_member" "proxy_client_key" {
  secret_id = google_secret_manager_secret.client_key.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.proxy.email}"
}

resource "google_secret_manager_secret_iam_member" "proxy_api_token" {
  secret_id = google_secret_manager_secret.api_token.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.proxy.email}"
}

resource "google_secret_manager_secret_iam_member" "proxy_internal_token" {
  secret_id = google_secret_manager_secret.internal_token.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.proxy.email}"
}

# Secret rotation function access (admin on all secrets)
resource "google_secret_manager_secret_iam_member" "secret_rotation_ca_cert" {
  secret_id = google_secret_manager_secret.ca_cert.id
  role      = "roles/secretmanager.admin"
  member    = "serviceAccount:${google_service_account.secret_rotation.email}"
}

resource "google_secret_manager_secret_iam_member" "secret_rotation_ca_key" {
  secret_id = google_secret_manager_secret.ca_key.id
  role      = "roles/secretmanager.admin"
  member    = "serviceAccount:${google_service_account.secret_rotation.email}"
}

resource "google_secret_manager_secret_iam_member" "secret_rotation_server_cert" {
  secret_id = google_secret_manager_secret.server_cert.id
  role      = "roles/secretmanager.admin"
  member    = "serviceAccount:${google_service_account.secret_rotation.email}"
}

resource "google_secret_manager_secret_iam_member" "secret_rotation_server_key" {
  secret_id = google_secret_manager_secret.server_key.id
  role      = "roles/secretmanager.admin"
  member    = "serviceAccount:${google_service_account.secret_rotation.email}"
}

resource "google_secret_manager_secret_iam_member" "secret_rotation_client_cert" {
  secret_id = google_secret_manager_secret.client_cert.id
  role      = "roles/secretmanager.admin"
  member    = "serviceAccount:${google_service_account.secret_rotation.email}"
}

resource "google_secret_manager_secret_iam_member" "secret_rotation_client_key" {
  secret_id = google_secret_manager_secret.client_key.id
  role      = "roles/secretmanager.admin"
  member    = "serviceAccount:${google_service_account.secret_rotation.email}"
}

resource "google_secret_manager_secret_iam_member" "secret_rotation_internal_token" {
  secret_id = google_secret_manager_secret.internal_token.id
  role      = "roles/secretmanager.admin"
  member    = "serviceAccount:${google_service_account.secret_rotation.email}"
}
