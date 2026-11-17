# Terraform and OpenTofu provider for NoctHost

Deploy and manage hourly-billed cloud servers on [NoctHost](https://nocthost.com) with Terraform or OpenTofu. NoctHost runs servers on Vultr, DigitalOcean, Hetzner, Linode and more from one account and one prepaid balance, topped up with crypto or a card.

- Website: https://nocthost.com
- API reference: https://nocthost.com/docs/api
- OpenAPI spec: https://nocthost.com/openapi.json

## What it manages

| Type | Name | Needs a token |
| --- | --- | --- |
| Resource | `nocthost_server` | yes |
| Data source | `nocthost_regions` | no |
| Data source | `nocthost_gpu_plans` | no |
| Data source | `nocthost_providers` | yes |
| Data source | `nocthost_servers` | yes |
| Data source | `nocthost_billing` | yes |

`nocthost_server` deploys a server and waits until it is `ACTIVE`, renames it in place when `label` changes, and replaces it when the size, region, cloud, image, password or SSH key changes. Destroying it deletes the server and waits until it is gone; billing stops the same hour.

## Usage

Create an API key in the NoctHost dashboard under Profile, API keys, then:

```hcl
terraform {
  required_providers {
    nocthost = {
      source = "DanielChernov7/nocthost"
    }
  }
}

# Reads NOCTHOST_TOKEN from the environment.
provider "nocthost" {}

variable "root_password" {
  type      = string
  sensitive = true
}

# Public catalog, no token needed.
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
```

```sh
export NOCTHOST_TOKEN=nh_...
terraform init
terraform apply -var 'root_password=...'
```

With OpenTofu, use `tofu init` and `tofu apply`; the configuration is the same.

The server costs money by the hour from your prepaid balance until you run `terraform destroy`. The root password is stored in state, so keep your state private.

### Sizes

The standard sizes work on every cloud: `vc2-1c-1gb` (Micro), `vc2-1c-2gb` (Starter), `vc2-2c-4gb` (Standard), `vc2-4c-8gb` (Pro), `vc2-6c-16gb` (Beast), `vc2-8c-32gb` (Titan), `vc2-16c-64gb` (Colossus) and `vc2-24c-96gb` (Leviathan). GPU sizes and prices come from the `nocthost_gpu_plans` data source. Every size with its current price is on https://nocthost.com/pricing.

### Provider configuration

| Argument | Environment variable | Default |
| --- | --- | --- |
| `token` | `NOCTHOST_TOKEN` | none |
| `endpoint` | `NOCTHOST_ENDPOINT` | `https://api.nocthost.com` |

More examples are in [`examples/`](examples) and the full reference is in [`docs/`](docs).

## Developing

Requires Go 1.25 or newer.

```sh
go build ./...
go vet ./...
go test ./...      # runs the provider through Terraform against a mock API
go generate ./...  # regenerates docs/ with tfplugindocs
```

The tests in `internal/provider` start an in-memory mock of the NoctHost API and drive the provider through a real `terraform` binary, so they never call the production API and never deploy anything. To run them with OpenTofu:

```sh
TF_ACC_TERRAFORM_PATH=$(which tofu) TF_ACC_PROVIDER_HOST=registry.opentofu.org go test ./...
```

To try a local build, point a CLI config at it:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/danielchernov7/nocthost" = "/path/to/your/GOBIN"
  }
  direct {}
}
```

## Releasing

Push a `v*` tag. The release workflow builds the binaries with GoReleaser, signs `SHA256SUMS` with the key in the `GPG_PRIVATE_KEY` secret and publishes a GitHub release that both registries read.

## License

[MPL-2.0](LICENSE)
