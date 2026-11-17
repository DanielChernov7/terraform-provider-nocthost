package provider

import (
	"context"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ---------------------------------------------------------------------------
// nocthost_providers

// NewProvidersDataSource returns the nocthost_providers data source.
func NewProvidersDataSource() datasource.DataSource { return &providersDataSource{} }

type providersDataSource struct{ baseDataSource }

type providersModel struct {
	Providers []providerItemModel `tfsdk:"providers"`
	Outages   []outageModel       `tfsdk:"outages"`
}

type providerItemModel struct {
	ID        types.String `tfsdk:"id"`
	Label     types.String `tfsdk:"label"`
	Available types.Bool   `tfsdk:"available"`
}

type outageModel struct {
	ProviderID types.String `tfsdk:"provider_id"`
	RegionID   types.String `tfsdk:"region_id"`
	Reason     types.String `tfsdk:"reason"`
}

func (d *providersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_providers"
}

func (d *providersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Cloud backends that take deploys right now, and regions that are temporarily paused on a backend " +
			"(`GET /servers/providers`). Check it before picking `provider_id` and `region_id`. Requires an API token.",
		Attributes: map[string]schema.Attribute{
			"providers": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Cloud backends.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":        schema.StringAttribute{Computed: true, MarkdownDescription: "ID to use as `provider_id` on `nocthost_server`."},
						"label":     schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
						"available": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the backend takes new deploys now."},
					},
				},
			},
			"outages": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Regions temporarily paused on a backend. A deploy there returns an error until it reopens.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"provider_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Cloud backend ID."},
						"region_id":   schema.StringAttribute{Computed: true, MarkdownDescription: "Region code."},
						"reason":      schema.StringAttribute{Computed: true, MarkdownDescription: "Human-readable reason."},
					},
				},
			},
		},
	}
}

func (d *providersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	out, err := d.client.GetProviders(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading NoctHost providers", err.Error())
		return
	}
	m := providersModel{Providers: []providerItemModel{}, Outages: []outageModel{}}
	for _, p := range out.Providers {
		m.Providers = append(m.Providers, providerItemModel{
			ID:        types.StringValue(p.ID),
			Label:     types.StringValue(p.Label),
			Available: types.BoolValue(p.Available),
		})
	}
	// Map iteration order is random; sort so the result is stable.
	pids := make([]string, 0, len(out.Outages))
	for pid := range out.Outages {
		pids = append(pids, pid)
	}
	sort.Strings(pids)
	for _, pid := range pids {
		rids := make([]string, 0, len(out.Outages[pid]))
		for rid := range out.Outages[pid] {
			rids = append(rids, rid)
		}
		sort.Strings(rids)
		for _, rid := range rids {
			m.Outages = append(m.Outages, outageModel{
				ProviderID: types.StringValue(pid),
				RegionID:   types.StringValue(rid),
				Reason:     types.StringValue(out.Outages[pid][rid]),
			})
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------------------------------------------------------------------
// nocthost_servers

// NewServersDataSource returns the nocthost_servers data source.
func NewServersDataSource() datasource.DataSource { return &serversDataSource{} }

type serversDataSource struct{ baseDataSource }

type serversModel struct {
	Servers []serverItemModel `tfsdk:"servers"`
}

type serverItemModel struct {
	ID          types.String  `tfsdk:"id"`
	Label       types.String  `tfsdk:"label"`
	Status      types.String  `tfsdk:"status"`
	ProviderID  types.String  `tfsdk:"provider_id"`
	RegionID    types.String  `tfsdk:"region_id"`
	PlanID      types.String  `tfsdk:"plan_id"`
	Image       types.String  `tfsdk:"image"`
	IPv4        types.String  `tfsdk:"ipv4"`
	HourlyPrice types.Float64 `tfsdk:"hourly_price"`
	CreatedAt   types.String  `tfsdk:"created_at"`
}

func (d *serversDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_servers"
}

func (d *serversDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "All servers on the account that are not destroyed (`GET /servers`). Requires an API token.",
		Attributes: map[string]schema.Attribute{
			"servers": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Servers, newest first.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":           schema.StringAttribute{Computed: true, MarkdownDescription: "Server ID."},
						"label":        schema.StringAttribute{Computed: true, MarkdownDescription: "Server name."},
						"status":       schema.StringAttribute{Computed: true, MarkdownDescription: "Server status."},
						"provider_id":  schema.StringAttribute{Computed: true, MarkdownDescription: "Cloud backend ID."},
						"region_id":    schema.StringAttribute{Computed: true, MarkdownDescription: "Region code."},
						"plan_id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Size ID."},
						"image":        schema.StringAttribute{Computed: true, MarkdownDescription: "Installed OS or app."},
						"ipv4":         schema.StringAttribute{Computed: true, MarkdownDescription: "Public IPv4 address."},
						"hourly_price": schema.Float64Attribute{Computed: true, MarkdownDescription: "Price per hour in USD."},
						"created_at":   schema.StringAttribute{Computed: true, MarkdownDescription: "Creation time (RFC 3339)."},
					},
				},
			},
		},
	}
}

func (d *serversDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := d.client.ListServers(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing NoctHost servers", err.Error())
		return
	}
	m := serversModel{Servers: []serverItemModel{}}
	for _, s := range list {
		item := serverItemModel{
			ID:         types.StringValue(s.ID),
			Label:      types.StringValue(s.Label),
			Status:     types.StringValue(s.Status),
			ProviderID: stringOrNull(s.Provider),
			RegionID:   stringOrNull(s.Region),
			PlanID:     stringOrNull(s.Plan),
			Image:      stringOrNull(s.Image),
			IPv4:       types.StringNull(),
			CreatedAt:  stringOrNull(s.CreatedAt),
		}
		if s.IPv4 != nil {
			item.IPv4 = stringOrNull(*s.IPv4)
		}
		if p, err := strconv.ParseFloat(s.HourlyPrice, 64); err == nil {
			item.HourlyPrice = types.Float64Value(p)
		} else {
			item.HourlyPrice = types.Float64Null()
		}
		m.Servers = append(m.Servers, item)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------------------------------------------------------------------
// nocthost_billing

// NewBillingDataSource returns the nocthost_billing data source.
func NewBillingDataSource() datasource.DataSource { return &billingDataSource{} }

type billingDataSource struct{ baseDataSource }

type billingModel struct {
	Balance     types.Float64 `tfsdk:"balance"`
	HourlySpend types.Float64 `tfsdk:"hourly_spend"`
}

func (d *billingDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_billing"
}

func (d *billingDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Prepaid balance and total hourly spend across active servers (`GET /billing`). Requires an API token.",
		Attributes: map[string]schema.Attribute{
			"balance":      schema.Float64Attribute{Computed: true, MarkdownDescription: "Prepaid balance, USD."},
			"hourly_spend": schema.Float64Attribute{Computed: true, MarkdownDescription: "Current spend per hour across active servers, USD."},
		},
	}
}

func (d *billingDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	b, err := d.client.GetBilling(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading NoctHost billing", err.Error())
		return
	}
	m := billingModel{Balance: parseMoney(b.Balance), HourlySpend: parseMoney(b.HourlySpend)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func parseMoney(v string) types.Float64 {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return types.Float64Null()
	}
	return types.Float64Value(f)
}
