package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// These tests run the real provider through Terraform (or OpenTofu, via
// TF_ACC_TERRAFORM_PATH) against an in-memory mock of the NoctHost API.
// They never touch the production API. resource.UnitTest runs without TF_ACC.

var testProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"nocthost": providerserver.NewProtocol6WithError(New("test")()),
}

func providerBlock(m *mockServer) string {
	return fmt.Sprintf(`
provider "nocthost" {
  endpoint = %q
  token    = %q
}
`, m.URL(), testToken)
}

const testPassword = "Str0ng-Passw0rd!"

func serverConfig(m *mockServer, label, plan string) string {
	return providerBlock(m) + fmt.Sprintf(`
resource "nocthost_server" "test" {
  label     = %q
  plan_id   = %q
  region_id = "ams"
  os_id     = 1743
  password  = %q
  ssh_key   = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl test@example"
}
`, label, plan, testPassword)
}

func TestAccServer_lifecycle(t *testing.T) {
	m := newMockServer(t)
	var firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			if n := m.liveCount(); n != 0 {
				return fmt.Errorf("%d servers still alive after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: serverConfig(m, "web-1", "vc2-1c-1gb"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("nocthost_server.test", "id"),
					resource.TestCheckResourceAttr("nocthost_server.test", "status", "ACTIVE"),
					resource.TestCheckResourceAttr("nocthost_server.test", "provider_id", "vultr"),
					resource.TestCheckResourceAttr("nocthost_server.test", "region_id", "ams"),
					resource.TestCheckResourceAttr("nocthost_server.test", "plan_id", "vc2-1c-1gb"),
					resource.TestCheckResourceAttr("nocthost_server.test", "hourly_price", "0.0137"),
					resource.TestCheckResourceAttr("nocthost_server.test", "access_type", "ssh"),
					resource.TestMatchResourceAttr("nocthost_server.test", "ipv4", regexp.MustCompile(`^203\.0\.113\.\d+$`)),
					resource.TestCheckResourceAttrWith("nocthost_server.test", "id", func(v string) error {
						firstID = v
						return nil
					}),
					func(_ *terraform.State) error {
						body := m.lastCreate()
						if body["osId"] != float64(1743) || body["password"] != testPassword || body["sshKey"] == nil {
							return fmt.Errorf("unexpected create body: %v", body)
						}
						if _, ok := body["providerId"]; ok {
							return fmt.Errorf("providerId should be omitted when not set, got %v", body["providerId"])
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "nocthost_server.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password", "ssh_key", "os_id", "timeouts"},
			},
			{
				// Label changes in place: no new server.
				Config: serverConfig(m, "web-renamed", "vc2-1c-1gb"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("nocthost_server.test", "label", "web-renamed"),
					resource.TestCheckResourceAttrWith("nocthost_server.test", "id", func(v string) error {
						if v != firstID {
							return fmt.Errorf("label change replaced the server: %s -> %s", firstID, v)
						}
						if n := m.createCount(); n != 1 {
							return fmt.Errorf("expected 1 create, got %d", n)
						}
						return nil
					}),
				),
			},
			{
				// Plan changes force a new server.
				Config: serverConfig(m, "web-renamed", "vc2-2c-4gb"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("nocthost_server.test", "plan_id", "vc2-2c-4gb"),
					resource.TestCheckResourceAttrWith("nocthost_server.test", "id", func(v string) error {
						if v == firstID {
							return fmt.Errorf("plan change did not replace the server")
						}
						if n := m.createCount(); n != 2 {
							return fmt.Errorf("expected 2 creates, got %d", n)
						}
						if n := m.liveCount(); n != 1 {
							return fmt.Errorf("expected the old server destroyed, %d alive", n)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccServer_explicitProvider(t *testing.T) {
	m := newMockServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(m) + `
resource "nocthost_server" "test" {
  label       = "hz-1"
  plan_id     = "cx22"
  region_id   = "nbg"
  provider_id = "hetzner"
  password    = "Str0ng-Passw0rd!"
  timeouts {
    create = "2m"
    delete = "2m"
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("nocthost_server.test", "provider_id", "hetzner"),
					func(_ *terraform.State) error {
						if got := m.lastCreate()["providerId"]; got != "hetzner" {
							return fmt.Errorf("providerId sent = %v", got)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccServer_disappears(t *testing.T) {
	m := newMockServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: serverConfig(m, "gone-1", "vc2-1c-1gb"),
				Check: func(_ *terraform.State) error {
					m.destroyOutOfBand()
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccServer_deployError(t *testing.T) {
	m := newMockServer(t)
	m.failDeploy = true
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      serverConfig(m, "bad-1", "vc2-1c-1gb"),
				ExpectError: regexp.MustCompile(`failed to deploy: provider_error`),
			},
		},
	})
}

func TestAccServer_createTimeout(t *testing.T) {
	m := newMockServer(t)
	m.neverActive = true
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerBlock(m) + `
resource "nocthost_server" "test" {
  label     = "slow-1"
  plan_id   = "vc2-1c-1gb"
  region_id = "ams"
  password  = "Str0ng-Passw0rd!"
  timeouts {
    create = "1s"
  }
}
`,
				ExpectError: regexp.MustCompile(`did not become ACTIVE before the create timeout`),
			},
		},
	})
}

func TestAccServer_apiValidationError(t *testing.T) {
	m := newMockServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      serverConfig(m, "x-1", "unknown-plan"),
				ExpectError: regexp.MustCompile(`HTTP 400\): Unknown plan or region`),
			},
		},
	})
	if n := m.liveCount(); n != 0 {
		t.Fatalf("expected no server, got %d", n)
	}
}

func TestAccServer_schemaValidation(t *testing.T) {
	m := newMockServer(t)
	cases := map[string]struct {
		body string
		err  string
	}{
		"label with space": {`label = "my server"
  plan_id = "vc2-1c-1gb"
  region_id = "ams"
  password = "Str0ng-Passw0rd!"`, `Latin letters`},
		"short password": {`label = "ok"
  plan_id = "vc2-1c-1gb"
  region_id = "ams"
  password = "short"`, `string length must be between 12 and 72`},
		"os and app": {`label = "ok"
  plan_id = "vc2-1c-1gb"
  region_id = "ams"
  password = "Str0ng-Passw0rd!"
  os_id = 1743
  app_slug = "wordpress"`, `cannot be specified when`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProviderFactories,
				Steps: []resource.TestStep{{
					Config:      providerBlock(m) + "resource \"nocthost_server\" \"test\" {\n  " + tc.body + "\n}\n",
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.err),
				}},
			})
		})
	}
	if n := m.createCount(); n != 0 {
		t.Fatalf("validation errors must not reach the API, got %d creates", n)
	}
}

func TestAccServer_noToken(t *testing.T) {
	m := newMockServer(t)
	t.Setenv("NOCTHOST_TOKEN", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "nocthost" {
  endpoint = %q
}
resource "nocthost_server" "test" {
  label     = "x"
  plan_id   = "vc2-1c-1gb"
  region_id = "ams"
  password  = "Str0ng-Passw0rd!"
}
`, m.URL()),
			ExpectError: regexp.MustCompile(`no API token configured`),
		}},
	})
}

func TestAccDataSource_regions(t *testing.T) {
	m := newMockServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			// No token: the public catalog must work without one.
			Config: fmt.Sprintf(`
provider "nocthost" {
  endpoint = %q
}
data "nocthost_regions" "all" {}
data "nocthost_regions" "hetzner" {
  provider_id = "hetzner"
}
data "nocthost_regions" "na" {
  continent = "north america"
}
`, m.URL()),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.nocthost_regions.all", "regions.#", "3"),
				resource.TestCheckResourceAttr("data.nocthost_regions.all", "generated_at", "2026-11-17T12:02:52.861Z"),
				resource.TestCheckResourceAttr("data.nocthost_regions.all", "regions.0.id", "ams"),
				resource.TestCheckResourceAttr("data.nocthost_regions.all", "regions.0.offers.#", "2"),
				resource.TestCheckResourceAttr("data.nocthost_regions.all", "regions.0.offers.1.provider_id", "linode"),
				resource.TestCheckResourceAttr("data.nocthost_regions.all", "regions.0.offers.1.from_monthly_usd", "61"),
				resource.TestCheckResourceAttr("data.nocthost_regions.hetzner", "regions.#", "1"),
				resource.TestCheckResourceAttr("data.nocthost_regions.hetzner", "regions.0.id", "nbg"),
				resource.TestCheckResourceAttr("data.nocthost_regions.na", "regions.#", "1"),
				resource.TestCheckResourceAttr("data.nocthost_regions.na", "regions.0.city", "New Jersey"),
			),
		}},
	})
}

func TestAccDataSource_gpuPlans(t *testing.T) {
	m := newMockServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "nocthost" {
  endpoint = %q
}
data "nocthost_gpu_plans" "all" {}
data "nocthost_gpu_plans" "linode" {
  provider_id = "linode"
}
`, m.URL()),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.nocthost_gpu_plans.all", "plans.#", "3"),
				resource.TestCheckResourceAttr("data.nocthost_gpu_plans.all", "plans.0.id", "vcg-a16-6c-60g-16vram"),
				resource.TestCheckResourceAttr("data.nocthost_gpu_plans.all", "plans.0.provider_id", "vultr"),
				resource.TestCheckNoResourceAttr("data.nocthost_gpu_plans.all", "plans.0.gpu_count"),
				resource.TestCheckResourceAttr("data.nocthost_gpu_plans.all", "plans.2.provider_id", "ovh"),
				resource.TestCheckResourceAttr("data.nocthost_gpu_plans.linode", "plans.#", "1"),
				resource.TestCheckResourceAttr("data.nocthost_gpu_plans.linode", "plans.0.gpu_count", "1"),
				resource.TestCheckResourceAttr("data.nocthost_gpu_plans.linode", "plans.0.regions.#", "2"),
				resource.TestCheckResourceAttr("data.nocthost_gpu_plans.linode", "plans.0.regions.1.id", "ord"),
			),
		}},
	})
}

func TestAccDataSource_account(t *testing.T) {
	m := newMockServer(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: serverConfig(m, "listed-1", "vc2-1c-1gb"),
			},
			{
				Config: serverConfig(m, "listed-1", "vc2-1c-1gb") + `
data "nocthost_providers" "p" {}
data "nocthost_servers" "all" {
  depends_on = [nocthost_server.test]
}
data "nocthost_billing" "b" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.nocthost_providers.p", "providers.#", "2"),
					resource.TestCheckResourceAttr("data.nocthost_providers.p", "providers.1.id", "hetzner"),
					resource.TestCheckResourceAttr("data.nocthost_providers.p", "outages.#", "2"),
					resource.TestCheckResourceAttr("data.nocthost_providers.p", "outages.0.region_id", "fra"),
					resource.TestCheckResourceAttr("data.nocthost_servers.all", "servers.#", "1"),
					resource.TestCheckResourceAttr("data.nocthost_servers.all", "servers.0.label", "listed-1"),
					resource.TestCheckResourceAttrPair("data.nocthost_servers.all", "servers.0.id", "nocthost_server.test", "id"),
					resource.TestCheckResourceAttr("data.nocthost_billing.b", "balance", "9.96"),
					resource.TestCheckResourceAttr("data.nocthost_billing.b", "hourly_spend", "0.012"),
				),
			},
		},
	})
}
