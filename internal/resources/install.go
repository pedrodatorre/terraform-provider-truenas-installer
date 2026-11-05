// Copyright (c) 2026 Pedro Datorre
// SPDX-License-Identifier: MPL-2.0

package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/pedrodatorre/terraform-provider-truenas-installer/internal/client"
	"github.com/pedrodatorre/terraform-provider-truenas-installer/internal/config"
)

var (
	_ resource.Resource              = (*installResource)(nil)
	_ resource.ResourceWithConfigure = (*installResource)(nil)
)

// NewInstall returns the truenas_installer_install resource.
func NewInstall(cfg *config.Config) resource.Resource {
	return &installResource{config: cfg}
}

type installResource struct {
	config *config.Config
}

func (r *installResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "truenas_installer_install"
}

func (r *installResource) Configure(_ context.Context, _ resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	// The provider shares its config through the pointer handed to NewInstall.
}

func (r *installResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Installs TrueNAS SCALE through the installer's JSON-RPC API.\n\n" +
			"`Create` connects to a booted installation ISO, calls `install`, and then optionally " +
			"shuts down or reboots the machine. This is a one-shot bootstrap: once the installer is " +
			"gone, `Read` keeps the state instead of removing the resource.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier (`<endpoint>#<disk>`).",
			},
			"disks": schema.ListAttribute{
				ElementType:         types.StringType,
				Required:            true,
				MarkdownDescription: "Disks to install TrueNAS onto, as reported by `list_disks` (for example `[\"sda\"]`).",
				PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
			},
			"wipe_disks": schema.ListAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "Additional disks to erase (for example stale boot-pool members).",
				PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
			},
			"set_pmbr": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Set the protective MBR boot flag so the installed system can later switch to UEFI.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"authentication": schema.SingleNestedAttribute{
				Required:            true,
				Sensitive:           true,
				MarkdownDescription: "Administrative account created on the installed system.",
				Attributes: map[string]schema.Attribute{
					"username": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Either `truenas_admin` or `root`.",
						Validators:          []validator.String{stringvalidator.OneOf("truenas_admin", "root")},
					},
					"password": schema.StringAttribute{
						Required:            true,
						Sensitive:           true,
						MarkdownDescription: "Password for the administrative account (at least 6 characters).",
						Validators:          []validator.String{stringvalidator.LengthAtLeast(6)},
					},
				},
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
			},
			"post_install": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Network configuration applied to the installed system.",
				Attributes: map[string]schema.Attribute{
					"network_interfaces": schema.ListNestedAttribute{
						Optional: true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"name":      schema.StringAttribute{Required: true, MarkdownDescription: "Interface name, e.g. `enp6s18`."},
								"ipv4_dhcp": schema.BoolAttribute{Optional: true, MarkdownDescription: "Use DHCP for IPv4."},
								"ipv6_auto": schema.BoolAttribute{Optional: true, MarkdownDescription: "Use SLAAC/DHCPv6 for IPv6."},
								"aliases": schema.ListNestedAttribute{
									Optional: true,
									NestedObject: schema.NestedAttributeObject{
										Attributes: map[string]schema.Attribute{
											"type":    schema.StringAttribute{Required: true, MarkdownDescription: "`INET` or `INET6`."},
											"address": schema.StringAttribute{Required: true, MarkdownDescription: "Static address."},
											"netmask": schema.Int64Attribute{Required: true, MarkdownDescription: "Prefix length."},
										},
									},
								},
							},
						},
					},
				},
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
			},
			"shutdown_on_complete": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Shut the installer down after a successful install (needed before converting the VM to a template).",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"reboot_on_complete": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Reboot into the installed system after a successful install. Conflicts with `shutdown_on_complete`.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"installation_completed": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the installer reported a completed installation.",
			},
			"installation_error": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Error reported by the installer, if any.",
			},
			"version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "TrueNAS version reported by the installer.",
			},
			"efi": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the installer booted in UEFI mode.",
			},
		},
	}
}

func (r *installResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan installModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.ShutdownOnComplete.ValueBool() && plan.RebootOnComplete.ValueBool() {
		resp.Diagnostics.AddError(
			"Conflicting power actions",
			"`shutdown_on_complete` and `reboot_on_complete` cannot both be true.",
		)
		return
	}

	request := expandInstall(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	cl, err := client.New(r.config.Endpoint, r.config.Timeout)
	if err != nil {
		resp.Diagnostics.AddError("Invalid installer endpoint", err.Error())
		return
	}
	connectCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	if err := cl.ConnectWithRetry(connectCtx, 5*time.Second); err != nil {
		var alreadyInstalled *client.AlreadyInstalledError
		if errors.As(err, &alreadyInstalled) {
			r.adoptInstalled(ctx, cl, &plan, request, resp)
			return
		}
		resp.Diagnostics.AddError(
			"Cannot reach the TrueNAS installer",
			"The installer's JSON-RPC server was not reachable at "+r.config.Endpoint+". "+
				"Confirm the ISO is booted, the VM has network, and the host resolves (mDNS or an explicit IP).\n\n"+err.Error(),
		)
		return
	}
	defer cl.Close()

	info, err := cl.SystemInfo(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read installer state", err.Error())
		return
	}
	if info.InstallationRunning {
		resp.Diagnostics.AddError("An installation is already running", "The installer is busy with another client.")
		return
	}

	// Log the disks the installer sees, and refuse an unknown target disk. With
	// an HBA passed through, the data disks are visible too; this makes the
	// choice explicit in the apply log.
	if disks, err := cl.ListDisks(ctx); err == nil {
		var names []string
		known := make(map[string]bool, len(disks))
		for _, disk := range disks {
			tflog.Info(ctx, "installer disk", map[string]any{
				"name":      disk.Name,
				"size":      disk.Size,
				"model":     disk.Model,
				"label":     disk.Label,
				"removable": disk.Removable,
			})
			names = append(names, disk.Name)
			known[disk.Name] = true
		}
		for _, name := range request.Disks {
			if !known[name] {
				resp.Diagnostics.AddError(
					"Unknown install disk",
					"Disk "+name+" is not reported by the installer. Available disks: "+strings.Join(names, ", "),
				)
				return
			}
		}
	}

	if info.InstallationCompleted {
		tflog.Info(ctx, "installer already reports a completed installation; skipping install")
	} else {
		tflog.Info(ctx, "starting TrueNAS installation", map[string]any{"disks": request.Disks})
		err = cl.Install(ctx, request, func(progress float64, message string) {
			tflog.Info(ctx, fmt.Sprintf("[%3.0f%%] %s", progress*100, message))
		})
		if err != nil {
			resp.Diagnostics.AddError("TrueNAS installation failed", err.Error())
			return
		}
	}

	switch {
	case plan.ShutdownOnComplete.ValueBool():
		if err := cl.Shutdown(ctx); err != nil {
			tflog.Warn(ctx, "shutdown returned an error (expected when the machine powers off)", map[string]any{"error": err.Error()})
		}
	case plan.RebootOnComplete.ValueBool():
		if err := cl.Reboot(ctx); err != nil {
			tflog.Warn(ctx, "reboot returned an error (expected when the machine reboots)", map[string]any{"error": err.Error()})
		}
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s#%s", r.config.Endpoint, strings.Join(request.Disks, ",")))
	plan.InstallationCompleted = types.BoolValue(true)
	plan.InstallationError = types.StringNull()
	plan.Version = types.StringValue(info.Version)
	plan.EFI = types.BoolValue(info.EFI)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// adoptInstalled handles the case where the installer is gone but the machine
// answers on 443: probe the TrueNAS API with the admin credentials and, if it
// authenticates, mark the resource complete instead of failing.
func (r *installResource) adoptInstalled(ctx context.Context, cl *client.Client, plan *installModel, request client.InstallRequest, resp *resource.CreateResponse) {
	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	version, err := client.ProbeInstalled(
		probeCtx,
		cl.Host(),
		request.Authentication.Username,
		request.Authentication.Password,
		r.config.InsecureSkipVerify,
		30*time.Second,
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"The installer is not running and the TrueNAS API did not confirm the installation",
			"Port 443 answers at "+cl.Host()+", but authenticating to the TrueNAS API failed: "+err.Error()+
				"\n\nIf you meant to rebuild, boot the installation ISO and wipe the boot disk before replacing this resource.",
		)
		return
	}

	resp.Diagnostics.AddWarning(
		"TrueNAS is already installed",
		"The installer is not running; the TrueNAS API answered at "+cl.Host()+" (version "+version+
			"). Marking the resource complete without reinstalling.",
	)

	plan.ID = types.StringValue(fmt.Sprintf("%s#%s", r.config.Endpoint, strings.Join(request.Disks, ",")))
	plan.InstallationCompleted = types.BoolValue(true)
	plan.InstallationError = types.StringNull()
	plan.Version = types.StringValue(version)
	plan.EFI = types.BoolNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *installResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state installModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cl, err := client.New(r.config.Endpoint, r.config.Timeout)
	if err != nil {
		return
	}

	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := cl.Connect(connectCtx); err != nil {
		// The installer is ephemeral: after install + shutdown it is gone. Keep
		// the state instead of removing the resource (which would reinstall).
		tflog.Debug(ctx, "installer not reachable; keeping state", map[string]any{"error": err.Error()})
		return
	}
	defer cl.Close()

	info, err := cl.SystemInfo(ctx)
	if err != nil {
		tflog.Debug(ctx, "cannot read installer state; keeping state", map[string]any{"error": err.Error()})
		return
	}

	state.InstallationCompleted = types.BoolValue(info.InstallationCompleted)
	state.Version = types.StringValue(info.Version)
	state.EFI = types.BoolValue(info.EFI)
	if info.InstallationError != nil {
		state.InstallationError = types.StringValue(*info.InstallationError)
	} else {
		state.InstallationError = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *installResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every configurable attribute is RequiresReplace; Update should not run.
	resp.Diagnostics.AddError(
		"Cannot update in place",
		"Changing an installation requires a new installer boot. Replace the resource instead.",
	)
}

func (r *installResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// No-op: the artefact is the Proxmox VM/template, which is managed elsewhere.
}

type installModel struct {
	ID                    types.String `tfsdk:"id"`
	Disks                 types.List   `tfsdk:"disks"`
	WipeDisks             types.List   `tfsdk:"wipe_disks"`
	SetPMBR               types.Bool   `tfsdk:"set_pmbr"`
	Authentication        types.Object `tfsdk:"authentication"`
	PostInstall           types.Object `tfsdk:"post_install"`
	ShutdownOnComplete    types.Bool   `tfsdk:"shutdown_on_complete"`
	RebootOnComplete      types.Bool   `tfsdk:"reboot_on_complete"`
	InstallationCompleted types.Bool   `tfsdk:"installation_completed"`
	InstallationError     types.String `tfsdk:"installation_error"`
	Version               types.String `tfsdk:"version"`
	EFI                   types.Bool   `tfsdk:"efi"`
}

type authModel struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

type postInstallModel struct {
	NetworkInterfaces types.List `tfsdk:"network_interfaces"`
}

type interfaceModel struct {
	Name     types.String `tfsdk:"name"`
	IPv4DHCP types.Bool   `tfsdk:"ipv4_dhcp"`
	IPv6Auto types.Bool   `tfsdk:"ipv6_auto"`
	Aliases  types.List   `tfsdk:"aliases"`
}

type aliasModel struct {
	Type    types.String `tfsdk:"type"`
	Address types.String `tfsdk:"address"`
	Netmask types.Int64  `tfsdk:"netmask"`
}

func expandInstall(ctx context.Context, plan installModel, diags *diag.Diagnostics) client.InstallRequest {
	var disks []string
	diags.Append(plan.Disks.ElementsAs(ctx, &disks, false)...)

	var wipeDisks []string
	if !plan.WipeDisks.IsNull() && !plan.WipeDisks.IsUnknown() {
		diags.Append(plan.WipeDisks.ElementsAs(ctx, &wipeDisks, false)...)
	}

	var auth authModel
	diags.Append(plan.Authentication.As(ctx, &auth, basetypes.ObjectAsOptions{})...)

	request := client.InstallRequest{
		Disks:     disks,
		WipeDisks: wipeDisks,
		SetPMBR:   plan.SetPMBR.ValueBool(),
		Authentication: &client.Authentication{
			Username: auth.Username.ValueString(),
			Password: auth.Password.ValueString(),
		},
	}

	if !plan.PostInstall.IsNull() && !plan.PostInstall.IsUnknown() {
		var post postInstallModel
		diags.Append(plan.PostInstall.As(ctx, &post, basetypes.ObjectAsOptions{})...)

		postInstall := &client.PostInstall{}
		if !post.NetworkInterfaces.IsNull() && !post.NetworkInterfaces.IsUnknown() {
			var interfaces []interfaceModel
			diags.Append(post.NetworkInterfaces.ElementsAs(ctx, &interfaces, false)...)

			for _, iface := range interfaces {
				entry := client.NetworkInterfaceConfig{Name: iface.Name.ValueString()}
				if !iface.IPv4DHCP.IsNull() && !iface.IPv4DHCP.IsUnknown() {
					value := iface.IPv4DHCP.ValueBool()
					entry.IPv4DHCP = &value
				}
				if !iface.IPv6Auto.IsNull() && !iface.IPv6Auto.IsUnknown() {
					value := iface.IPv6Auto.ValueBool()
					entry.IPv6Auto = &value
				}
				if !iface.Aliases.IsNull() && !iface.Aliases.IsUnknown() {
					var aliases []aliasModel
					diags.Append(iface.Aliases.ElementsAs(ctx, &aliases, false)...)
					for _, alias := range aliases {
						entry.Aliases = append(entry.Aliases, client.Alias{
							Type:    alias.Type.ValueString(),
							Address: alias.Address.ValueString(),
							Netmask: int(alias.Netmask.ValueInt64()),
						})
					}
				}
				postInstall.NetworkInterfaces = append(postInstall.NetworkInterfaces, entry)
			}
		}
		request.PostInstall = postInstall
	}

	return request
}
