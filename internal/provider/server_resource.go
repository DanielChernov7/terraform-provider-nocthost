package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/DanielChernov7/terraform-provider-nocthost/internal/client"
)

// pollInterval is how often the server is polled while waiting. The API
// docs ask for roughly every 5 seconds. Tests lower it.
var pollInterval = 5 * time.Second

const (
	defaultCreateTimeout = 30 * time.Minute
	defaultDeleteTimeout = 15 * time.Minute
)

var (
	_ resource.Resource                = &serverResource{}
	_ resource.ResourceWithConfigure   = &serverResource{}
	_ resource.ResourceWithImportState = &serverResource{}
)

// NewServerResource returns the nocthost_server resource.
func NewServerResource() resource.Resource {
	return &serverResource{}
}

type serverResource struct {
	client *client.Client
}

type serverModel struct {
	ID          types.String   `tfsdk:"id"`
	Label       types.String   `tfsdk:"label"`
	PlanID      types.String   `tfsdk:"plan_id"`
	RegionID    types.String   `tfsdk:"region_id"`
	ProviderID  types.String   `tfsdk:"provider_id"`
	OSID        types.Int64    `tfsdk:"os_id"`
	AppSlug     types.String   `tfsdk:"app_slug"`
	Password    types.String   `tfsdk:"password"`
	SSHKey      types.String   `tfsdk:"ssh_key"`
	Status      types.String   `tfsdk:"status"`
	IPv4        types.String   `tfsdk:"ipv4"`
	HourlyPrice types.Float64  `tfsdk:"hourly_price"`
	Image       types.String   `tfsdk:"image"`
	AccessType  types.String   `tfsdk:"access_type"`
	CreatedAt   types.String   `tfsdk:"created_at"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func (r *serverResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (r *serverResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}

	resp.Schema = schema.Schema{
		MarkdownDescription: "A NoctHost cloud server, billed by the hour from the prepaid balance. " +
			"Creating it deploys a real server (`POST /servers`) and waits until it is `ACTIVE`; destroying it deletes the server " +
			"(billing stops the same hour) and waits until it is gone. Changing anything except `label` replaces the server.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Server ID.",
				Computed:            true,
				PlanModifiers:       keep,
			},
			"label": schema.StringAttribute{
				MarkdownDescription: "Server name, also used as the hostname. Latin letters, digits, dot, dash and underscore, up to 64 characters. Can be changed in place.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 64),
					stringvalidator.RegexMatches(regexp.MustCompile(`^[a-zA-Z0-9._-]+$`), "must contain only Latin letters, digits, dot, dash and underscore"),
				},
			},
			"plan_id": schema.StringAttribute{
				MarkdownDescription: "Size to deploy. The standard sizes work on every cloud: `vc2-1c-1gb` (Micro), `vc2-1c-2gb` (Starter), `vc2-2c-4gb` (Standard), `vc2-4c-8gb` (Pro), `vc2-6c-16gb` (Beast), `vc2-8c-32gb` (Titan), `vc2-16c-64gb` (Colossus), `vc2-24c-96gb` (Leviathan). GPU sizes come from the `nocthost_gpu_plans` data source; all sizes and prices are on https://nocthost.com/pricing.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"region_id": schema.StringAttribute{
				MarkdownDescription: "Region (city) code, for example `ams`. See the `nocthost_regions` data source.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"provider_id": schema.StringAttribute{
				MarkdownDescription: "Cloud backend: `vultr`, `digitalocean`, `hetzner`, `linode` and others listed by the `nocthost_providers` data source. If omitted, the API uses its default backend (`vultr`); set it explicitly for predictable plans.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"os_id": schema.Int64Attribute{
				MarkdownDescription: "Operating system ID from the catalog (default Ubuntu 22.04, `1743`). Conflicts with `app_slug`.",
				Optional:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
					int64validator.ConflictsWith(path.MatchRoot("app_slug")),
				},
			},
			"app_slug": schema.StringAttribute{
				MarkdownDescription: "One-click app to install instead of a plain OS (Vultr only). Conflicts with `os_id`.",
				Optional:            true,
				PlanModifiers:       replace,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Root password: 12 to 72 Latin characters with at least three of lowercase, uppercase, digits and symbols, no spaces. " +
					"Required by the API for almost every image. It is stored in Terraform state, so protect your state.",
				Optional:      true,
				Sensitive:     true,
				PlanModifiers: replace,
				Validators: []validator.String{
					stringvalidator.LengthBetween(12, 72),
					stringvalidator.RegexMatches(regexp.MustCompile(`^[\x21-\x7E]+$`), "must contain only printable Latin characters without spaces"),
				},
			},
			"ssh_key": schema.StringAttribute{
				MarkdownDescription: "Public SSH key installed for root at first boot (the whole line, starting with `ssh-ed25519`, `ssh-rsa` or `ecdsa-sha2-nistp256`).",
				Optional:            true,
				PlanModifiers:       replace,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Server status: `PROVISIONING`, `ACTIVE`, `STOPPED`, `SUSPENDED`, `ARCHIVED`, `DESTROYING`, `DESTROYED` or `ERROR`.",
				Computed:            true,
			},
			"ipv4": schema.StringAttribute{
				MarkdownDescription: "Public IPv4 address.",
				Computed:            true,
			},
			"hourly_price": schema.Float64Attribute{
				MarkdownDescription: "Price per hour in USD.",
				Computed:            true,
			},
			"image": schema.StringAttribute{
				MarkdownDescription: "Installed OS or app, as a readable name.",
				Computed:            true,
			},
			"access_type": schema.StringAttribute{
				MarkdownDescription: "How to log in: `ssh` or `rdp` (Windows).",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Creation time (RFC 3339).",
				Computed:            true,
				PlanModifiers:       keep,
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{
				Create: true,
				Delete: true,
			}),
		},
	}
}

func (r *serverResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *serverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createTimeout, diags := plan.Timeouts.Create(ctx, defaultCreateTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	in := client.CreateServerRequest{
		Label:      plan.Label.ValueString(),
		PlanID:     plan.PlanID.ValueString(),
		RegionID:   plan.RegionID.ValueString(),
		ProviderID: plan.ProviderID.ValueString(),
		AppSlug:    plan.AppSlug.ValueString(),
		Password:   plan.Password.ValueString(),
		SSHKey:     plan.SSHKey.ValueString(),
	}
	if !plan.OSID.IsNull() && !plan.OSID.IsUnknown() {
		v := plan.OSID.ValueInt64()
		in.OSID = &v
	}

	srv, err := r.client.CreateServer(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Error creating NoctHost server", err.Error())
		return
	}
	tflog.Info(ctx, "NoctHost server accepted, waiting until ACTIVE", map[string]any{"id": srv.ID})

	// Save the ID right away: if waiting fails, the server is still recorded
	// (as tainted) so it is not orphaned and keeps no hidden cost.
	applyServer(&plan, srv)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	waitCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	srv, err = r.waitActive(waitCtx, srv.ID)
	if srv != nil {
		applyServer(&plan, srv)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for NoctHost server to become ACTIVE", err.Error())
	}
}

func (r *serverResource) waitActive(ctx context.Context, id string) (*client.Server, error) {
	var last *client.Server
	for {
		srv, err := r.client.GetServer(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return last, fmt.Errorf("server %s did not become ACTIVE before the create timeout (last status %s)", id, statusOf(last))
			}
			return last, err
		}
		last = srv
		switch srv.Status {
		case client.StatusActive:
			return srv, nil
		case client.StatusProvisioning:
		case client.StatusError:
			reason := "unknown reason"
			if srv.FailureReason != nil && *srv.FailureReason != "" {
				reason = *srv.FailureReason
			}
			return srv, fmt.Errorf("server %s failed to deploy: %s. It is marked tainted; the next apply destroys and recreates it", id, reason)
		default:
			return srv, fmt.Errorf("server %s reached unexpected status %s while deploying", id, srv.Status)
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("server %s did not become ACTIVE before the create timeout (last status %s)", id, statusOf(last))
		case <-time.After(pollInterval):
		}
	}
}

func statusOf(s *client.Server) string {
	if s == nil {
		return "unknown"
	}
	return s.Status
}

func (r *serverResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	srv, err := r.client.GetServer(ctx, state.ID.ValueString())
	if client.IsNotFound(err) || (err == nil && (srv.Status == client.StatusDestroyed || srv.Status == client.StatusDestroying)) {
		tflog.Warn(ctx, "NoctHost server is gone, removing from state", map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading NoctHost server", err.Error())
		return
	}

	applyServer(&state, srv)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	var srv *client.Server
	var err error
	if !plan.Label.Equal(state.Label) {
		srv, err = r.client.RenameServer(ctx, id, plan.Label.ValueString())
	} else {
		srv, err = r.client.GetServer(ctx, id)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating NoctHost server", err.Error())
		return
	}

	plan.ID = state.ID
	applyServer(&plan, srv)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteTimeout, diags := state.Timeouts.Delete(ctx, defaultDeleteTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	err := r.client.DeleteServer(ctx, id)
	switch {
	case err == nil, client.IsConflict(err):
		// 409 means it is already being destroyed: just wait for it.
	case client.IsNotFound(err):
		return
	default:
		resp.Diagnostics.AddError("Error deleting NoctHost server", err.Error())
		return
	}

	waitCtx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()
	for {
		srv, err := r.client.GetServer(waitCtx, id)
		if client.IsNotFound(err) || (err == nil && srv.Status == client.StatusDestroyed) {
			return
		}
		if err != nil {
			if waitCtx.Err() != nil {
				resp.Diagnostics.AddError("Timeout waiting for NoctHost server deletion",
					fmt.Sprintf("server %s was not destroyed before the delete timeout", id))
				return
			}
			resp.Diagnostics.AddError("Error waiting for NoctHost server deletion", err.Error())
			return
		}
		select {
		case <-waitCtx.Done():
			resp.Diagnostics.AddError("Timeout waiting for NoctHost server deletion",
				fmt.Sprintf("server %s was not destroyed before the delete timeout (last status %s)", id, srv.Status))
			return
		case <-time.After(pollInterval):
		}
	}
}

func (r *serverResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// applyServer copies API fields into the model. Inputs the API does not echo
// back (os_id, app_slug, password, ssh_key) are left as they are.
func applyServer(m *serverModel, s *client.Server) {
	m.ID = types.StringValue(s.ID)
	if s.Label != "" {
		m.Label = types.StringValue(s.Label)
	}
	if s.Plan != "" {
		m.PlanID = types.StringValue(s.Plan)
	}
	if s.Region != "" {
		m.RegionID = types.StringValue(s.Region)
	}
	if s.Provider != "" {
		m.ProviderID = types.StringValue(s.Provider)
	} else if m.ProviderID.IsUnknown() {
		m.ProviderID = types.StringNull()
	}
	m.Status = types.StringValue(s.Status)
	if s.IPv4 != nil && *s.IPv4 != "" {
		m.IPv4 = types.StringValue(*s.IPv4)
	} else {
		m.IPv4 = types.StringNull()
	}
	if p, err := strconv.ParseFloat(s.HourlyPrice, 64); err == nil {
		m.HourlyPrice = types.Float64Value(p)
	} else {
		m.HourlyPrice = types.Float64Null()
	}
	m.Image = stringOrNull(s.Image)
	m.AccessType = stringOrNull(s.AccessType)
	m.CreatedAt = stringOrNull(s.CreatedAt)
}

func stringOrNull(v string) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
}
