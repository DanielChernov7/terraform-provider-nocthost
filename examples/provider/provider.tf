terraform {
  required_providers {
    nocthost = {
      source = "DanielChernov7/nocthost"
    }
  }
}

# The token can also come from the NOCTHOST_TOKEN environment variable.
# Create one in the NoctHost dashboard under Profile, API keys.
provider "nocthost" {
  token = var.nocthost_token
}

variable "nocthost_token" {
  type      = string
  sensitive = true
}
