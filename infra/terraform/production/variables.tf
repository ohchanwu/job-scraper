variable "canonical_network_config" {
  description = "Private configuration of the adopted canonical network."
  sensitive   = true

  type = object({
    vpc = object({
      cidr_block                           = string
      enable_dns_hostnames                 = bool
      enable_dns_support                   = bool
      enable_network_address_usage_metrics = bool
      instance_tenancy                     = string
    })
    public_subnets = map(object({
      availability_zone                              = string
      cidr_block                                     = string
      assign_ipv6_address_on_creation                = bool
      enable_dns64                                   = bool
      enable_resource_name_dns_a_record_on_launch    = bool
      enable_resource_name_dns_aaaa_record_on_launch = bool
      map_public_ip_on_launch                        = bool
      private_dns_hostname_type_on_launch            = string
    }))
  })

  validation {
    condition = (
      toset(keys(var.canonical_network_config.public_subnets)) ==
      toset(["public_a", "public_b", "public_c", "public_d"])
    )
    error_message = "Canonical public subnet keys must be public_a through public_d."
  }
}

variable "canonical_import_ids" {
  description = "Private identifiers used only during canonical network adoption."
  sensitive   = true

  type = object({
    vpc                 = string
    internet_gateway    = string
    public_subnets      = map(string)
    public_route_table  = string
    public_ipv4_default = string
  })

  validation {
    condition = (
      toset(keys(var.canonical_import_ids.public_subnets)) ==
      toset(["public_a", "public_b", "public_c", "public_d"]) &&
      alltrue([
        trimspace(var.canonical_import_ids.vpc) != "",
        trimspace(var.canonical_import_ids.internet_gateway) != "",
        trimspace(var.canonical_import_ids.public_route_table) != "",
        trimspace(var.canonical_import_ids.public_ipv4_default) != "",
      ]) &&
      alltrue([
        for subnet_id in values(var.canonical_import_ids.public_subnets) :
        trimspace(subnet_id) != ""
      ])
    )
    error_message = "Canonical import IDs must contain eight populated network bindings."
  }
}
