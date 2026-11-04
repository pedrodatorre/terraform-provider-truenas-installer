---
page_title: "truenas_installer_install Resource"
description: |-
  Installs TrueNAS SCALE through the installer's JSON-RPC API.
---

# truenas_installer_install

Installs TrueNAS SCALE through the installer's JSON-RPC API. `Create` connects
to a booted installation ISO, calls `install`, and then optionally shuts down or
reboots the machine.

This is a one-shot bootstrap. Once the installer is gone, `Read` keeps the state
instead of removing the resource, and `Delete` is a no-op.

## Example Usage

```terraform
resource "truenas_installer_install" "this" {
  disks    = ["sda"]
  set_pmbr = true

  authentication = {
    username = "truenas_admin"
    password = var.truenas_password
  }

  post_install = {
    network_interfaces = [
      { name = "enp6s18", ipv4_dhcp = true }
    ]
  }

  shutdown_on_complete = true
}
```

## Schema

### Required

- `disks` (List of String) Disks to install onto, as reported by `list_disks`.
- `authentication` (Attributes) Administrative account. (see below)

### Optional

- `wipe_disks` (List of String) Additional disks to erase.
- `set_pmbr` (Boolean) Set the protective MBR boot flag. Defaults to `true`.
- `post_install` (Attributes) Network configuration for the installed system.
  (see below)
- `shutdown_on_complete` (Boolean) Shut down after a successful install.
  Defaults to `true`.
- `reboot_on_complete` (Boolean) Reboot into the installed system. Conflicts with
  `shutdown_on_complete`. Defaults to `false`.

### Read-Only

- `id` (String) `<endpoint>#<disk>`.
- `installation_completed` (Boolean)
- `installation_error` (String)
- `version` (String)
- `efi` (Boolean)

### `authentication`

- `username` (String, Required) `truenas_admin` or `root`.
- `password` (String, Required, Sensitive) At least 6 characters.

### `post_install`

- `network_interfaces` (List of Object, Optional)
  - `name` (String, Required)
  - `ipv4_dhcp` (Boolean, Optional)
  - `ipv6_auto` (Boolean, Optional)
  - `aliases` (List of Object, Optional)
    - `type` (String, Required) `INET` or `INET6`.
    - `address` (String, Required)
    - `netmask` (Number, Required)
