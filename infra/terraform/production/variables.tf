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

variable "private_database_config" {
  description = "Private configuration of the production database tier."
  sensitive   = true

  type = object({
    private_subnets = map(object({
      availability_zone = string
      cidr_block        = string
    }))
    database_identifier       = string
    database_name             = string
    master_username           = string
    final_snapshot_identifier = string
    runtime_secret_name       = string
    recovery_bucket_name      = string
  })

  validation {
    condition = (
      toset(keys(var.private_database_config.private_subnets)) ==
      toset(["database_a", "database_b"])
    )
    error_message = "Private database subnet keys must be database_a and database_b."
  }

  validation {
    condition = (
      alltrue([
        for subnet in values(var.private_database_config.private_subnets) :
        trimspace(subnet.availability_zone) != ""
      ]) &&
      length(distinct(values(var.private_database_config.private_subnets)[*].availability_zone)) == 2
    )
    error_message = "Private database subnets must use two distinct non-empty availability zones."
  }

  validation {
    condition = (
      alltrue([
        for subnet in values(var.private_database_config.private_subnets) :
        trimspace(subnet.cidr_block) != ""
      ]) &&
      length(distinct(values(var.private_database_config.private_subnets)[*].cidr_block)) == 2
    )
    error_message = "Private database subnets must use two distinct non-empty CIDR blocks."
  }

  validation {
    condition = alltrue([
      for name in [
        var.private_database_config.database_identifier,
        var.private_database_config.database_name,
        var.private_database_config.master_username,
        var.private_database_config.final_snapshot_identifier,
        var.private_database_config.runtime_secret_name,
        var.private_database_config.recovery_bucket_name,
      ] : trimspace(name) != ""
    ])
    error_message = "Private database resource names must not be empty."
  }

  validation {
    condition     = can(regex("^[a-z][a-z0-9_]*$", var.private_database_config.database_name))
    error_message = "The database name must use PostgreSQL identifier form."
  }

  validation {
    condition     = can(regex("^[a-z][a-z0-9_]*$", var.private_database_config.master_username))
    error_message = "The master username must use PostgreSQL identifier form."
  }
}
