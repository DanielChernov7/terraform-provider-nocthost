# GPU sizes with at least 48 GB of VRAM in Europe. No API token needed.
data "nocthost_gpu_plans" "big" {
  min_vram_gb = 48
  continent   = "Europe"
}

output "gpu_options" {
  value = [for p in data.nocthost_gpu_plans.big.plans : {
    plan_id     = p.id
    provider_id = p.provider_id
    gpu         = p.gpu_model
    hourly_usd  = p.price_per_hour_usd
    regions     = [for r in p.regions : r.id]
  }]
}
