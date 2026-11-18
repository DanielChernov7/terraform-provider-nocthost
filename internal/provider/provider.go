// Package provider implements the NoctHost Terraform/OpenTofu provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/DanielChernov7/terraform-provider-nocthost/internal/client"
)

var _ provider.Provider = &nocthostProvider{}

type nocthostProvider struct {
	version string
}

type providerModel struct {
	Token    types.String `tfsdk:"token"`
	Endpoint types.String `tfsdk:"endpoint"`
}

// New returns a provider factory for the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &nocthostProvider{version: version}
	}
}

func (p *nocthostProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "nocthost"
	resp.Version = p.version
}

func (p *nocthostProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The NoctHost provider deploys and manages hourly-billed cloud servers on [NoctHost](https://nocthost.com) " +
			"(Hetzner, Vultr, DigitalOcean, Linode, OVHcloud and Cherry Servers bare metal, from one account and one prepaid balance). " +
			"See the [API reference](https://nocthost.com/docs/api).",
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				MarkdownDescription: "Personal API key (starts with `nh_`), created in the NoctHost dashboard under Profile, API keys. " +
					"Can also be set with the `NOCTHOST_TOKEN` environment variable. Not needed for the public catalog data sources " +
					"(`nocthost_regions`, `nocthost_gpu_plans`).",
				Optional:  true,
				Sensitive: true,
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "API base URL. Defaults to `" + client.DefaultEndpoint + "`. " +
					"Can also be set with the `NOCTHOST_ENDPOINT` environment variable.",
				Optional: true,
			},
		},
	}
}

func (p *nocthostProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.Token.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Unknown NoctHost API token",
			"The provider cannot be configured with a token that is unknown until apply. Use a static value or the NOCTHOST_TOKEN environment variable.")
	}
	if cfg.Endpoint.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("endpoint"), "Unknown NoctHost API endpoint",
			"The provider cannot be configured with an endpoint that is unknown until apply.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	token := os.Getenv("NOCTHOST_TOKEN")
	if !cfg.Token.IsNull() {
		token = cfg.Token.ValueString()
	}
	endpoint := os.Getenv("NOCTHOST_ENDPOINT")
	if !cfg.Endpoint.IsNull() && cfg.Endpoint.ValueString() != "" {
		endpoint = cfg.Endpoint.ValueString()
	}

	c := client.New(endpoint, token, "terraform-provider-nocthost/"+p.version)
	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *nocthostProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewServerResource,
	}
}

func (p *nocthostProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewRegionsDataSource,
		NewGPUPlansDataSource,
		NewProvidersDataSource,
		NewServersDataSource,
		NewBillingDataSource,
	}
}
