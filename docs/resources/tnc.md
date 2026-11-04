---
page_title: "truenas_installer_tnc Resource"
description: |-
  Enables and configures TrueNAS Connect on the installer.
---

# truenas_installer_tnc

Enables and configures TrueNAS Connect before installing.

`configure_tnc` can only run once per boot, and registration is completed by a
human in a browser. The resource exposes `registration_uri` and the
`initialization_*` fields as computed values instead of finalising registration.

## Example Usage

```terraform
resource "truenas_installer_tnc" "this" {
  enabled = true
}
```

## Schema

### Required

- `enabled` (Boolean) Enable TrueNAS Connect.

### Optional

- `account_service_base_url` (String)
- `leca_service_base_url` (String)
- `heartbeat_service_base_url` (String)
- `tnc_base_url` (String)

### Read-Only

- `id` (String) `tnc:<endpoint>`.
- `claim_token` (String, Sensitive)
- `claim_token_expiration` (Number)
- `system_id` (String)
- `truenas_version` (String)
- `initialization_in_progress` (Boolean)
- `initialization_completed` (Boolean)
- `initialization_error` (String)
- `registration_uri` (String)
