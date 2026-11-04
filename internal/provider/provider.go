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
	Endpoint types.String `tfsdk:"endpoint"`
	Timeout  types.String `tfsdk:"timeout"`
}
