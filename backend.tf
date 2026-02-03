# Backend configuration
# Configure your GCS backend bucket during terraform init:
#   terraform init -backend-config="bucket=YOUR-BUCKET-NAME"
#
# Or create a backend.conf file with:
#   bucket = "your-bucket-name"
#   prefix = "private-llm"
# Then run: terraform init -backend-config=backend.conf

terraform {
  backend "gcs" {
    prefix = "private-llm"
  }
}
