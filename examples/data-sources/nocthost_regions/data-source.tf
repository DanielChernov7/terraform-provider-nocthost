# Every region that can take a deploy right now. No API token needed.
data "nocthost_regions" "all" {}

# Only Hetzner regions in Europe.
data "nocthost_regions" "hetzner_eu" {
  provider_id = "hetzner"
  continent   = "Europe"
}

output "hetzner_eu_cities" {
  value = [for r in data.nocthost_regions.hetzner_eu.regions : "${r.id}: ${r.city}"]
}
