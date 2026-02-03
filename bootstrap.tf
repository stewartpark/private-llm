# ──────────────────────────────────────────────────────────────
# Bootstrap Secrets (Cloud-Agnostic)
# ──────────────────────────────────────────────────────────────
#
# Generates initial mTLS certificates and tokens using Terraform providers.
# NO CLOUD DEPENDENCIES - runs first to bootstrap the infrastructure.
#
# These are BOOTSTRAP values only.
# The rotation function overwrites certs/tokens in Secret Manager.
# API token is user-managed and persists.
#
# Generated secrets:
# - CA certificate (10-year validity, 4096-bit RSA)
# - CA private key
# - Server certificate (1-week validity, signed by CA)
# - Server private key
# - Client certificate (1-week validity, signed by CA)
# - Client private key
# - Internal token (64 hex chars)
# - API token (64 chars)
#
# ──────────────────────────────────────────────────────────────

# ──────────────────────────────────────────────────────────────
# CA Certificate (10-year validity)
# ──────────────────────────────────────────────────────────────

resource "tls_private_key" "ca" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "tls_self_signed_cert" "ca" {
  private_key_pem = tls_private_key.ca.private_key_pem

  subject {
    common_name = "Private LLM CA"
  }

  validity_period_hours = 87600 # 10 years
  is_ca_certificate     = true

  allowed_uses = [
    "cert_signing",
    "crl_signing",
  ]
}

# ──────────────────────────────────────────────────────────────
# Server Certificate (1-week validity, signed by CA)
# ──────────────────────────────────────────────────────────────

resource "tls_private_key" "server" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "tls_cert_request" "server" {
  private_key_pem = tls_private_key.server.private_key_pem

  subject {
    common_name = "private-llm-server"
  }

  dns_names = ["private-llm-server"]
}

resource "tls_locally_signed_cert" "server" {
  cert_request_pem   = tls_cert_request.server.cert_request_pem
  ca_private_key_pem = tls_private_key.ca.private_key_pem
  ca_cert_pem        = tls_self_signed_cert.ca.cert_pem

  validity_period_hours = 168 # 1 week

  allowed_uses = [
    "key_encipherment",
    "digital_signature",
    "server_auth",
  ]
}

# ──────────────────────────────────────────────────────────────
# Client Certificate (1-week validity, signed by CA)
# ──────────────────────────────────────────────────────────────

resource "tls_private_key" "client" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "tls_cert_request" "client" {
  private_key_pem = tls_private_key.client.private_key_pem

  subject {
    common_name = "private-llm-client"
  }
}

resource "tls_locally_signed_cert" "client" {
  cert_request_pem   = tls_cert_request.client.cert_request_pem
  ca_private_key_pem = tls_private_key.ca.private_key_pem
  ca_cert_pem        = tls_self_signed_cert.ca.cert_pem

  validity_period_hours = 168 # 1 week

  allowed_uses = [
    "key_encipherment",
    "digital_signature",
    "client_auth",
  ]
}

# ──────────────────────────────────────────────────────────────
# Internal Token (64 hex characters)
# ──────────────────────────────────────────────────────────────

resource "random_password" "internal_token" {
  length  = 64
  special = false
  upper   = false
  lower   = true
  numeric = true
}

# ──────────────────────────────────────────────────────────────
# API Token (64 characters, user-managed)
# ──────────────────────────────────────────────────────────────

resource "random_password" "api_token" {
  length  = 64
  special = false
}

# ──────────────────────────────────────────────────────────────
# Bootstrap Locals (exposed to modules)
# ──────────────────────────────────────────────────────────────

locals {
  # These are BOOTSTRAP values only.
  # The rotation function overwrites certs/tokens in Secret Manager.
  bootstrap_ca_cert_pem     = tls_self_signed_cert.ca.cert_pem
  bootstrap_ca_key_pem      = tls_private_key.ca.private_key_pem
  bootstrap_server_cert_pem = tls_locally_signed_cert.server.cert_pem
  bootstrap_server_key_pem  = tls_private_key.server.private_key_pem
  bootstrap_client_cert_pem = tls_locally_signed_cert.client.cert_pem
  bootstrap_client_key_pem  = tls_private_key.client.private_key_pem
  bootstrap_internal_token  = random_password.internal_token.result
  bootstrap_api_token       = random_password.api_token.result # User-managed, persists
}
