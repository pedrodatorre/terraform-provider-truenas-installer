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
      {
        name      = "enp6s18"
        ipv4_dhcp = true
      }
    ]
  }

  shutdown_on_complete = true
  reboot_on_complete   = false
}

variable "truenas_password" {
  type      = string
  sensitive = true
}
