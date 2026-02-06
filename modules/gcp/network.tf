# ──────────────────────────────────────────────────────────────
# Network Configuration
# ──────────────────────────────────────────────────────────────

# Dedicated VPC
resource "google_compute_network" "main" {
  name                    = "private-llm-vpc"
  auto_create_subnetworks = false
  depends_on              = [google_project_service.apis]
}

# Subnet
resource "google_compute_subnetwork" "main" {
  name          = "private-llm-subnet"
  ip_cidr_range = var.subnet_cidr
  region        = var.region
  network       = google_compute_network.main.id

  # Enable Private Google Access for Cloud Functions to reach GCP APIs
  private_ip_google_access = true
}


# Note: Direct VPC egress does NOT require a VPC Access Connector
# Cloud Functions Gen 2 will get IPs directly from the subnet
