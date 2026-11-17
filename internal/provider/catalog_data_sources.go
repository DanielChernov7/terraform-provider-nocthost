package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/DanielChernov7/terraform-provider-nocthost/internal/client"
)

// baseDataSource carries the configured client.
type baseDataSource struct {
	client *client.Client
}

func (d *baseDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	d.client = c
}

// ---------------------------------------------------------------------------
// nocthost_regions

// NewRegionsDataSource returns the nocthost_regions data source.
func NewRegionsDataSource() datasource.DataSource { return &regionsDataSource{} }

type regionsDataSource struct{ baseDataSource }

type regionsModel struct {
	ProviderID  types.String  `tfsdk:"provider_id"`
	Continent   types.String  `tfsdk:"continent"`
	GeneratedAt types.String  `tfsdk:"generated_at"`
	Regions     []regionModel `tfsdk:"regions"`
}

type regionModel struct {
	ID        types.String       `tfsdk:"id"`
	City      types.String       `tfsdk:"city"`
	Continent types.String       `tfsdk:"continent"`
	Offers    []regionOfferModel `tfsdk:"offers"`
}

type regionOfferModel struct {
	ProviderID     types.String  `tfsdk:"provider_id"`
	ProviderName   types.String  `tfsdk:"provider_name"`
	Category       types.String  `tfsdk:"category"`
	CategoryName   types.String  `tfsdk:"category_name"`
	Sizes          types.Int64   `tfsdk:"sizes"`
	FromHourlyUSD  types.Float64 `tfsdk:"from_hourly_usd"`
	FromMonthlyUSD types.Float64 `tfsdk:"from_monthly_usd"`
}

func (d *regionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_regions"
}

func (d *regionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Regions (cities) where a server can be deployed right now, with the clouds and size categories offered in each " +
			"and the starting price. Reads the public catalog (`GET /public/v1/deployable`), so no API token is needed.",
		Attributes: map[string]schema.Attribute{
			"provider_id": schema.StringAttribute{
				MarkdownDescription: "Only return regions offered by this cloud backend (for example `hetzner`), and only that cloud's offers in them.",
				Optional:            true,
			},
			"continent": schema.StringAttribute{
				MarkdownDescription: "Only return regions on this continent (for example `Europe`, `North America`, `Asia`). Case-insensitive.",
				Optional:            true,
			},
			"generated_at": schema.StringAttribute{
				MarkdownDescription: "When the catalog was generated.",
				Computed:            true,
			},
			"regions": schema.ListNestedAttribute{
				MarkdownDescription: "Matching regions.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":        schema.StringAttribute{Computed: true, MarkdownDescription: "Region code to use as `region_id` on `nocthost_server`."},
						"city":      schema.StringAttribute{Computed: true, MarkdownDescription: "City name."},
						"continent": schema.StringAttribute{Computed: true, MarkdownDescription: "Continent."},
						"offers": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Clouds and size categories available in this region.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"provider_id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Cloud backend ID to use as `provider_id` on `nocthost_server`."},
									"provider_name":    schema.StringAttribute{Computed: true, MarkdownDescription: "Cloud backend name."},
									"category":         schema.StringAttribute{Computed: true, MarkdownDescription: "Size category ID: `shared`, `dedicated`, `gpu` or `metal`."},
									"category_name":    schema.StringAttribute{Computed: true, MarkdownDescription: "Size category name."},
									"sizes":            schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of sizes offered."},
									"from_hourly_usd":  schema.Float64Attribute{Computed: true, MarkdownDescription: "Cheapest hourly price in USD."},
									"from_monthly_usd": schema.Float64Attribute{Computed: true, MarkdownDescription: "Cheapest monthly price in USD."},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *regionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg regionsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	doc, err := d.client.GetDeployable(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading NoctHost regions", err.Error())
		return
	}

	wantProvider := strings.ToLower(cfg.ProviderID.ValueString())
	wantContinent := strings.ToLower(cfg.Continent.ValueString())

	cfg.GeneratedAt = stringOrNull(doc.GeneratedAt)
	cfg.Regions = []regionModel{}
	for _, loc := range doc.Locations {
		if wantContinent != "" && strings.ToLower(loc.Continent) != wantContinent {
			continue
		}
		offers := []regionOfferModel{}
		for _, o := range loc.Offers {
			if wantProvider != "" && strings.ToLower(o.CloudID) != wantProvider {
				continue
			}
			offers = append(offers, regionOfferModel{
				ProviderID:     types.StringValue(o.CloudID),
				ProviderName:   types.StringValue(o.Cloud),
				Category:       types.StringValue(o.Category),
				CategoryName:   types.StringValue(o.CategoryName),
				Sizes:          types.Int64Value(o.Sizes),
				FromHourlyUSD:  types.Float64Value(o.FromHourlyUSD),
				FromMonthlyUSD: types.Float64Value(o.FromMonthlyUSD),
			})
		}
		if wantProvider != "" && len(offers) == 0 {
			continue
		}
		cfg.Regions = append(cfg.Regions, regionModel{
			ID:        types.StringValue(loc.RegionID),
			City:      types.StringValue(loc.City),
			Continent: types.StringValue(loc.Continent),
			Offers:    offers,
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// ---------------------------------------------------------------------------
// nocthost_gpu_plans

// NewGPUPlansDataSource returns the nocthost_gpu_plans data source.
func NewGPUPlansDataSource() datasource.DataSource { return &gpuPlansDataSource{} }

type gpuPlansDataSource struct{ baseDataSource }

type gpuPlansModel struct {
	GPUModel     types.String   `tfsdk:"gpu_model"`
	MinVRAMGB    types.Float64  `tfsdk:"min_vram_gb"`
	Continent    types.String   `tfsdk:"continent"`
	MaxHourlyUSD types.Float64  `tfsdk:"max_hourly_usd"`
	ProviderID   types.String   `tfsdk:"provider_id"`
	Plans        []gpuPlanModel `tfsdk:"plans"`
}

type gpuPlanModel struct {
	ID               types.String     `tfsdk:"id"`
	ProviderID       types.String     `tfsdk:"provider_id"`
	ProviderName     types.String     `tfsdk:"provider_name"`
	GPUModel         types.String     `tfsdk:"gpu_model"`
	GPUCount         types.Int64      `tfsdk:"gpu_count"`
	Fractional       types.Bool       `tfsdk:"fractional"`
	VRAMGBPerGPU     types.Float64    `tfsdk:"vram_gb_per_gpu"`
	VRAMGBTotal      types.Float64    `tfsdk:"vram_gb_total"`
	VCPU             types.Int64      `tfsdk:"vcpu"`
	RAMGB            types.Float64    `tfsdk:"ram_gb"`
	DiskGB           types.Float64    `tfsdk:"disk_gb"`
	PricePerHourUSD  types.Float64    `tfsdk:"price_per_hour_usd"`
	PricePerMonthUSD types.Float64    `tfsdk:"price_per_month_usd"`
	Regions          []gpuRegionModel `tfsdk:"regions"`
}

type gpuRegionModel struct {
	ID               types.String  `tfsdk:"id"`
	City             types.String  `tfsdk:"city"`
	Continent        types.String  `tfsdk:"continent"`
	PricePerHourUSD  types.Float64 `tfsdk:"price_per_hour_usd"`
	PricePerMonthUSD types.Float64 `tfsdk:"price_per_month_usd"`
}

// cloudIDs maps the catalog's cloud names to provider IDs used on deploy.
var cloudIDs = map[string]string{
	"vultr":          "vultr",
	"digitalocean":   "digitalocean",
	"hetzner":        "hetzner",
	"linode":         "linode",
	"akamai":         "linode",
	"ovhcloud":       "ovh",
	"ovh":            "ovh",
	"cherry servers": "cherry",
}

func cloudID(name string) string {
	k := strings.ToLower(strings.TrimSpace(name))
	if id, ok := cloudIDs[k]; ok {
		return id
	}
	return strings.ReplaceAll(k, " ", "")
}

// cloudName is the inverse of cloudID for the API's `cloud` filter.
func cloudName(id string) string {
	switch strings.ToLower(id) {
	case "vultr":
		return "Vultr"
	case "digitalocean":
		return "DigitalOcean"
	case "hetzner":
		return "Hetzner"
	case "linode":
		return "Linode"
	case "ovh":
		return "OVHcloud"
	case "cherry":
		return "Cherry Servers"
	}
	return id
}

func (d *gpuPlansDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gpu_plans"
}

func (d *gpuPlansDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	regionAttrs := map[string]schema.Attribute{
		"id":                  schema.StringAttribute{Computed: true, MarkdownDescription: "Region code to use as `region_id`."},
		"city":                schema.StringAttribute{Computed: true, MarkdownDescription: "City name."},
		"continent":           schema.StringAttribute{Computed: true, MarkdownDescription: "Continent."},
		"price_per_hour_usd":  schema.Float64Attribute{Computed: true, MarkdownDescription: "Hourly price in this region, USD."},
		"price_per_month_usd": schema.Float64Attribute{Computed: true, MarkdownDescription: "Monthly price in this region, USD."},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU sizes with specs, hourly prices and the regions they deploy in. Reads the public catalog " +
			"(`GET /public/v1/gpus`), so no API token is needed.",
		Attributes: map[string]schema.Attribute{
			"gpu_model":      schema.StringAttribute{Optional: true, MarkdownDescription: "Only GPUs whose model contains this text, for example `L40S`."},
			"min_vram_gb":    schema.Float64Attribute{Optional: true, MarkdownDescription: "Only sizes with at least this much total VRAM, GB."},
			"continent":      schema.StringAttribute{Optional: true, MarkdownDescription: "Only regions on this continent, for example `Europe`."},
			"max_hourly_usd": schema.Float64Attribute{Optional: true, MarkdownDescription: "Only regions where the hourly price is at most this, USD."},
			"provider_id":    schema.StringAttribute{Optional: true, MarkdownDescription: "Only sizes from this cloud backend, for example `linode`."},
			"plans": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Matching GPU sizes.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":                  schema.StringAttribute{Computed: true, MarkdownDescription: "Size ID to use as `plan_id` on `nocthost_server`."},
						"provider_id":         schema.StringAttribute{Computed: true, MarkdownDescription: "Cloud backend ID to use as `provider_id`."},
						"provider_name":       schema.StringAttribute{Computed: true, MarkdownDescription: "Cloud backend name."},
						"gpu_model":           schema.StringAttribute{Computed: true, MarkdownDescription: "GPU model."},
						"gpu_count":           schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of GPUs, null for shared or partitioned cards."},
						"fractional":          schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the size gets a fraction of a GPU."},
						"vram_gb_per_gpu":     schema.Float64Attribute{Computed: true, MarkdownDescription: "VRAM per GPU, GB."},
						"vram_gb_total":       schema.Float64Attribute{Computed: true, MarkdownDescription: "Total VRAM, GB."},
						"vcpu":                schema.Int64Attribute{Computed: true, MarkdownDescription: "vCPU count."},
						"ram_gb":              schema.Float64Attribute{Computed: true, MarkdownDescription: "RAM, GB."},
						"disk_gb":             schema.Float64Attribute{Computed: true, MarkdownDescription: "Disk, GB."},
						"price_per_hour_usd":  schema.Float64Attribute{Computed: true, MarkdownDescription: "Lowest hourly price across regions, USD."},
						"price_per_month_usd": schema.Float64Attribute{Computed: true, MarkdownDescription: "Lowest monthly price across regions, USD."},
						"regions": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Regions where this size deploys, with the price there.",
							NestedObject:        schema.NestedAttributeObject{Attributes: regionAttrs},
						},
					},
				},
			},
		},
	}
}

func (d *gpuPlansDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg gpuPlansModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	f := client.GPUFilter{
		GPUModel:     cfg.GPUModel.ValueString(),
		MinVRAMGB:    cfg.MinVRAMGB.ValueFloat64(),
		Continent:    cfg.Continent.ValueString(),
		MaxHourlyUSD: cfg.MaxHourlyUSD.ValueFloat64(),
	}
	if id := cfg.ProviderID.ValueString(); id != "" {
		f.Cloud = cloudName(id)
	}
	offers, err := d.client.GetGPUs(ctx, f)
	if err != nil {
		resp.Diagnostics.AddError("Error reading NoctHost GPU plans", err.Error())
		return
	}

	cfg.Plans = []gpuPlanModel{}
	for _, o := range offers {
		pid := cloudID(o.Cloud)
		if want := strings.ToLower(cfg.ProviderID.ValueString()); want != "" && pid != want {
			continue
		}
		regions := make([]gpuRegionModel, 0, len(o.Regions))
		for _, r := range o.Regions {
			regions = append(regions, gpuRegionModel{
				ID:               types.StringValue(r.ID),
				City:             types.StringValue(r.City),
				Continent:        types.StringValue(r.Continent),
				PricePerHourUSD:  types.Float64Value(r.PricePerHourUSD),
				PricePerMonthUSD: types.Float64Value(r.PricePerMonthUSD),
			})
		}
		p := gpuPlanModel{
			ID:               types.StringValue(o.ID),
			ProviderID:       types.StringValue(pid),
			ProviderName:     types.StringValue(o.Cloud),
			GPUModel:         types.StringValue(o.GPUModel),
			GPUCount:         types.Int64PointerValue(o.GPUCount),
			Fractional:       types.BoolValue(o.Fractional),
			VRAMGBPerGPU:     types.Float64PointerValue(o.VRAMGBPerGPU),
			VRAMGBTotal:      types.Float64Value(o.VRAMGBTotal),
			VCPU:             types.Int64Value(o.VCPU),
			RAMGB:            types.Float64Value(o.RAMGB),
			DiskGB:           types.Float64Value(o.DiskGB),
			PricePerHourUSD:  types.Float64Value(o.PricePerHourUSD),
			PricePerMonthUSD: types.Float64Value(o.PricePerMonthUSD),
			Regions:          regions,
		}
		cfg.Plans = append(cfg.Plans, p)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
