data "nocthost_providers" "current" {}

output "available_providers" {
  value = [for p in data.nocthost_providers.current.providers : p.id if p.available]
}
