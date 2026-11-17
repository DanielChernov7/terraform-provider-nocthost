data "nocthost_servers" "all" {}

output "server_ips" {
  value = { for s in data.nocthost_servers.all.servers : s.label => s.ipv4 }
}
