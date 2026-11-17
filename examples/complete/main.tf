# A small, complete example: pick a Hetzner region in Europe from the live
# catalog and deploy one server there with your SSH key.
#
#   export NOCTHOST_TOKEN=nh_...
#   terraform init && terraform apply -var 'root_password=...'
#
# The server is billed by the hour from your prepaid balance until you run
# terraform destroy.

terraform {
  required_providers {
    nocthost = {
      source = "DanielChernov7/nocthost"
    }
  }
}

provider "nocthost" {}

variable "root_password" {
  type      = string
  sensitive = true
}

data "nocthost_regions" "hetzner_eu" {
  provider_id = "hetzner"
  continent   = "Europe"
}

resource "nocthost_server" "app" {
  label       = "app-1"
  provider_id = "hetzner"
  plan_id     = "vc2-1c-2gb" # Starter: 1 vCPU, 2 GB RAM
  region_id   = data.nocthost_regions.hetzner_eu.regions[0].id
  password    = var.root_password
  ssh_key     = file("~/.ssh/id_ed25519.pub")
}

output "ssh" {
  value = "ssh root@${nocthost_server.app.ipv4}"
}
