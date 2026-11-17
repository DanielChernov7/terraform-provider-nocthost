resource "nocthost_server" "web" {
  label       = "web-1"
  provider_id = "hetzner"
  plan_id     = "vc2-1c-2gb" # Starter: 1 vCPU, 2 GB RAM
  region_id   = "fra"
  os_id       = 1743 # Ubuntu 22.04
  password    = var.root_password
  ssh_key     = file("~/.ssh/id_ed25519.pub")

  timeouts {
    create = "20m"
    delete = "10m"
  }
}

variable "root_password" {
  type      = string
  sensitive = true
}

output "web_ip" {
  value = nocthost_server.web.ipv4
}
