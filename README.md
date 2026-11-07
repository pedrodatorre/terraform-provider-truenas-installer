# terraform-provider-truenas-installer

A Terraform/OpenTofu provider for **bootstrapping TrueNAS SCALE**: it drives the
JSON-RPC API that `truenas-installer.service` exposes on a booted installation
ISO. No Packer, no `boot_command`, no keystrokes.

> Registry source: `pedrodatorre/truenas-installer`

## How it works

The TrueNAS SCALE ISO ships an **enabled** `truenas-installer.service` that
speaks JSON-RPC 2.0 over WebSocket:

- nginx listens on `:80` and proxies `/ws` to `127.0.0.1:8080`;
- the installer advertises itself as `truenas-installer.local` over mDNS;
- methods used: `system_info`, `list_disks`, `install`, `shutdown`, `reboot`,
  `tnc_config`, `configure_tnc`, `tnc_registration_uri`.

This provider turns that API into Terraform resources.

## Requirements

- TrueNAS SCALE installation ISO (tested against 25.10 "Goldeye").
- The machine running `terraform`/`tofu` must reach the booted installer (same
  L2 for mDNS, or an explicit IP).
- Terraform >= 1.6 / OpenTofu >= 1.6 (plugin protocol 6).

## Installation

```hcl
terraform {
  required_providers {
    # IMPORTANT: the resource types are prefixed `truenas_`, so the local name
    # MUST be `truenas`. The source is what selects this provider.
    truenas = {
      source = "pedrodatorre/truenas-installer"
    }
  }
}

provider "truenas" {
  endpoint = "truenas-installer.local" # or an explicit IP
  timeout  = "45m"                     # optional, default 30m
}
```

`endpoint` accepts `host`, `host:port`, `ws://host/ws`, `wss://host/ws`,
`http://host` or `https://host`. A bare host becomes `ws://host:80/ws`.

> **Registry:** the provider is published on both the Terraform Registry and the
> OpenTofu Registry. A bare source works on either: Terraform resolves it to
> `registry.terraform.io`, OpenTofu to `registry.opentofu.org`.

## Resources

### `truenas_installer_install`

```hcl
resource "truenas_installer_install" "this" {
  disks      = ["sda"]
  wipe_disks = []
  set_pmbr   = true

  authentication = {
    username = "truenas_admin"
    password = var.truenas_password
  }

  post_install = {
    network_interfaces = [
      { name = "enp6s18", ipv4_dhcp = true }
    ]
  }

  shutdown_on_complete = true  # default
  reboot_on_complete   = false # default
}
```

One-shot semantics:

- `Create` connects, reads `system_info`, calls `install`, then shuts down or
  reboots the machine.
- `Read` keeps the state when the installer is no longer reachable (that is the
  normal state after a successful install) instead of removing the resource.
- `Delete` is a no-op. The artefact is the Proxmox VM/template, managed by your
  infrastructure provider.
- Changing any input replaces the resource: an install requires a fresh boot.

### `truenas_installer_tnc`

Enables and configures TrueNAS Connect **before** installing:

```hcl
resource "truenas_installer_tnc" "this" {
  enabled = true
}
```

`configure_tnc` can only run once per boot, and registration is finished by a
human in a browser. The resource therefore exposes `claim_token`,
`registration_uri` and the `initialization_*` fields as computed values.

## Development

```sh
make build      # ./terraform-provider-truenas-installer
make test       # go test ./...
make install    # copies the binary into ~/.terraform.d/plugins
```

`make install` + `dev_overrides` lets OpenTofu use a local build:

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "pedrodatorre/truenas-installer" = "/home/you/.terraform.d/plugins/terraform-provider-truenas-installer"
  }
  direct {}
}
```

With `dev_overrides` you do not run `tofu init`; go straight to `tofu plan`.

## Publishing to the registry

The Terraform Registry requires the GitHub repository to be named
`terraform-provider-<type>`, so publish this project as
**`github.com/pedrodatorre/terraform-provider-truenas-installer`** (the registry
source is then `pedrodatorre/truenas-installer`). The release workflow signs the
checksums with GPG; configure the `GPG_PRIVATE_KEY` and `PASSPHRASE` repository
secrets.

## License

MPL-2.0. See [LICENSE](LICENSE).
