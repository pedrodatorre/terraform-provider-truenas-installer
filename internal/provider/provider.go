// Copyright (c) 2026 Pedro Datorre
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/pedrodatorre/terraform-provider-truenas-installer/internal/config"
	"github.com/pedrodatorre/terraform-provider-truenas-installer/internal/resources"
)

type truenasInstallerProvider struct {
	version string
	config  *config.Config
}

// New returns the provider factory used by providerserver.Serve.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &truenasInstallerProvider{
			version: version,
			config:  &config.Config{Timeout: 30 * time.Minute},
		}
	}
}

func (p *truenasInstallerProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "truenas-installer"
	resp.Version = p.version
}

func (p *truenasInstallerProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Drives the JSON-RPC API exposed by `truenas-installer.service` on a booted " +
			"TrueNAS SCALE installation ISO. It installs TrueNAS over the network - no Packer, no keystrokes.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "WebSocket endpoint of the installer. Accepts `host`, `host:port`, " +
					"`ws://host/ws` or `wss://host/ws`. Plain hosts default to `ws://host:80/ws`. " +
					"The ISO advertises itself as `truenas-installer.local` over mDNS.",
			},
			"timeout": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Per-call timeout as a Go duration (for example `45m`). " +
					"Defaults to `30m`, which is generous enough for a full install.",
			},
			"insecure_skip_verify": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Skip TLS verification when probing the installed TrueNAS API " +
					"(`wss://<host>/api/current`). Defaults to `true` because TrueNAS uses a " +
					"self-signed certificate by default.",
			},
			"boot_timeout": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "How long to wait for the installer to boot before failing, as a Go " +
					"duration (for example `10m`). Defaults to `10m`. The same window is used to retry " +
					"the TrueNAS API probe and to wait for the installed system after a reboot.",
			},
			"wait_for_reboot": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "After `reboot_on_complete`, wait for the installed TrueNAS API to " +
					"answer before returning. Defaults to `true`, so the module only finishes when " +
					"TrueNAS is actually up.",
			},
		},
	}
}

func (p *truenasInstallerProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var configModel providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &configModel)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if configModel.Endpoint.IsNull() || configModel.Endpoint.IsUnknown() {
		resp.Diagnostics.AddError(
			"Missing installer endpoint",
			"Set `endpoint` to the host running the TrueNAS installer, e.g. `truenas-installer.local`.",
		)
		return
	}

	p.config.Endpoint = configModel.Endpoint.ValueString()
	p.config.Timeout = 30 * time.Minute
	if !configModel.Timeout.IsNull() && !configModel.Timeout.IsUnknown() && configModel.Timeout.ValueString() != "" {
		timeout, err := time.ParseDuration(configModel.Timeout.ValueString())
		if err != nil {
			resp.Diagnostics.AddError(
				"Invalid timeout",
				"The `timeout` attribute must be a Go duration such as `30m` or `1h`: "+err.Error(),
			)
			return
		}
		p.config.Timeout = timeout
	}

	p.config.InsecureSkipVerify = true
	if !configModel.InsecureSkipVerify.IsNull() && !configModel.InsecureSkipVerify.IsUnknown() {
		p.config.InsecureSkipVerify = configModel.InsecureSkipVerify.ValueBool()
	}

	p.config.BootTimeout = 10 * time.Minute
	if !configModel.BootTimeout.IsNull() && !configModel.BootTimeout.IsUnknown() && configModel.BootTimeout.ValueString() != "" {
		bootTimeout, err := time.ParseDuration(configModel.BootTimeout.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Invalid boot_timeout", "The `boot_timeout` attribute must be a Go duration such as `10m`: "+err.Error())
			return
		}
		p.config.BootTimeout = bootTimeout
	}

	p.config.WaitForReboot = true
	if !configModel.WaitForReboot.IsNull() && !configModel.WaitForReboot.IsUnknown() {
		p.config.WaitForReboot = configModel.WaitForReboot.ValueBool()
	}
}

func (p *truenasInstallerProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource { return resources.NewInstall(p.config) },
		func() resource.Resource { return resources.NewTNC(p.config) },
	}
}

func (p *truenasInstallerProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

type providerModel struct {
	Endpoint           types.String `tfsdk:"endpoint"`
	Timeout            types.String `tfsdk:"timeout"`
	InsecureSkipVerify types.Bool   `tfsdk:"insecure_skip_verify"`
	BootTimeout        types.String `tfsdk:"boot_timeout"`
	WaitForReboot      types.Bool   `tfsdk:"wait_for_reboot"`
}
