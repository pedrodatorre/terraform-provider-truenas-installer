---
page_title: "truenas-installer Provider"
description: |-
  Bootstraps TrueNAS SCALE by driving the installation ISO's JSON-RPC API.
---

# truenas-installer Provider

Bootstraps TrueNAS SCALE by driving the JSON-RPC 2.0 / WebSocket API exposed by
`truenas-installer.service` on a booted installation ISO.

No Packer, no `boot_command`, no keystrokes.

## Example Usage

```terraform
terraform {
  required_providers {
    # The resource types are prefixed `truenas_`, so the local name MUST be
    # `truenas`. The source selects this provider.
    truenas = {
      source = "pedrodatorre/truenas-installer"
    }
  }
}

provider "truenas" {
  endpoint = "truenas-installer.local"
  timeout  = "45m"
}
```

## Schema

### Required

- `endpoint` (String) WebSocket endpoint of the installer. Accepts `host`,
  `host:port`, `ws://host/ws` or `wss://host/ws`. A bare host defaults to
  `ws://host:80/ws`. The ISO advertises itself as `truenas-installer.local` over
  mDNS.

### Optional

- `timeout` (String) Per-call timeout as a Go duration (`30m`, `1h`). Defaults
  to `30m`.
