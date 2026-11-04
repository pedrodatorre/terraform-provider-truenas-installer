terraform {
  required_providers {
    # The resource types are prefixed `truenas_`, so the local name MUST be
    # `truenas`. The source selects this provider.
    truenas = {
      source = "registry.terraform.io/pedrodatorre/truenas-installer"
    }
  }
}

provider "truenas" {
  endpoint = "truenas-installer.local"
  timeout  = "45m"
}
