# ──────────────────────────────────────────────────────────────
# Cloud Provider Module Instantiation
# ──────────────────────────────────────────────────────────────
#
# Instantiates the selected cloud provider module.
# Currently supports GCP, with AWS/Azure planned for future.
#
# To switch cloud providers, comment out the GCP module
# and uncomment the desired provider module.
#
# ──────────────────────────────────────────────────────────────

module "gcp" {
  source = "./modules/gcp"

  # GCP Configuration
  project_id = var.project_id
  region     = var.region
  zone       = var.zone

  # VM Configuration
  vm_name            = var.vm_name
  machine_type       = var.machine_type
  default_model      = var.default_model
  context_length     = var.context_length
  idle_timeout       = var.idle_timeout
  subnet_cidr        = var.subnet_cidr
  enable_external_ip = var.enable_external_ip

  # Bootstrap Secrets (from root) - overwritten by rotation function
  bootstrap_ca_cert_pem     = local.bootstrap_ca_cert_pem
  bootstrap_ca_key_pem      = local.bootstrap_ca_key_pem
  bootstrap_server_cert_pem = local.bootstrap_server_cert_pem
  bootstrap_server_key_pem  = local.bootstrap_server_key_pem
  bootstrap_client_cert_pem = local.bootstrap_client_cert_pem
  bootstrap_client_key_pem  = local.bootstrap_client_key_pem
  bootstrap_internal_token  = local.bootstrap_internal_token
}

# ──────────────────────────────────────────────────────────────
# Future: AWS Module
# ──────────────────────────────────────────────────────────────
#
# module "aws" {
#   source = "./modules/aws"
#
#   # AWS Configuration
#   region = var.aws_region
#
#   # VM Configuration
#   vm_name        = var.vm_name
#   instance_type  = var.aws_instance_type
#   default_model  = var.default_model
#   context_length = var.context_length
#   idle_timeout   = var.idle_timeout
#
#   # Bootstrap Secrets (from root)
#   bootstrap_ca_cert_pem     = local.bootstrap_ca_cert_pem
#   bootstrap_ca_key_pem      = local.bootstrap_ca_key_pem
#   bootstrap_server_cert_pem = local.bootstrap_server_cert_pem
#   bootstrap_server_key_pem  = local.bootstrap_server_key_pem
#   bootstrap_client_cert_pem = local.bootstrap_client_cert_pem
#   bootstrap_client_key_pem  = local.bootstrap_client_key_pem
#   bootstrap_internal_token  = local.bootstrap_internal_token
# }
