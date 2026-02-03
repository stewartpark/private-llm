# ──────────────────────────────────────────────────────────────
# GCP Provider Configuration
# ──────────────────────────────────────────────────────────────

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google-beta"
      version = "~> 7.17.0"
    }
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# Data source for project number (needed for service agent emails)
data "google_project" "project" {
  project_id = var.project_id
}
