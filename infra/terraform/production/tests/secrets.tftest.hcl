mock_provider "aws" {}

override_resource {
  target          = aws_route_table.public
  override_during = plan
  values = {
    id = "rtb-jobcron-test-only"
  }
}

variables {
  canonical_network_config = {
    vpc = {
      cidr_block                           = "10.255.0.0/24"
      enable_dns_hostnames                 = true
      enable_dns_support                   = true
      enable_network_address_usage_metrics = false
      instance_tenancy                     = "default"
    }
    public_subnets = {
      public_a = {
        availability_zone                              = "example-1a"
        cidr_block                                     = "10.255.0.0/28"
        assign_ipv6_address_on_creation                = false
        enable_dns64                                   = false
        enable_resource_name_dns_a_record_on_launch    = false
        enable_resource_name_dns_aaaa_record_on_launch = false
        map_public_ip_on_launch                        = true
        private_dns_hostname_type_on_launch            = "ip-name"
      }
      public_b = {
        availability_zone                              = "example-1b"
        cidr_block                                     = "10.255.0.16/28"
        assign_ipv6_address_on_creation                = false
        enable_dns64                                   = false
        enable_resource_name_dns_a_record_on_launch    = false
        enable_resource_name_dns_aaaa_record_on_launch = false
        map_public_ip_on_launch                        = true
        private_dns_hostname_type_on_launch            = "ip-name"
      }
      public_c = {
        availability_zone                              = "example-1c"
        cidr_block                                     = "10.255.0.32/28"
        assign_ipv6_address_on_creation                = false
        enable_dns64                                   = false
        enable_resource_name_dns_a_record_on_launch    = false
        enable_resource_name_dns_aaaa_record_on_launch = false
        map_public_ip_on_launch                        = true
        private_dns_hostname_type_on_launch            = "ip-name"
      }
      public_d = {
        availability_zone                              = "example-1d"
        cidr_block                                     = "10.255.0.48/28"
        assign_ipv6_address_on_creation                = false
        enable_dns64                                   = false
        enable_resource_name_dns_a_record_on_launch    = false
        enable_resource_name_dns_aaaa_record_on_launch = false
        map_public_ip_on_launch                        = true
        private_dns_hostname_type_on_launch            = "ip-name"
      }
    }
  }

  private_database_config = {
    private_subnets = {
      database_a = {
        availability_zone = "example-1a"
        cidr_block        = "10.255.0.64/28"
      }
      database_b = {
        availability_zone = "example-1b"
        cidr_block        = "10.255.0.80/28"
      }
    }
    database_identifier       = "jobcron-test-only"
    database_name             = "jobcron"
    master_username           = "jobcron_admin"
    final_snapshot_identifier = "jobcron-final-test-only"
    runtime_secret_name       = "jobcron/test/runtime"
    recovery_bucket_name      = "jobcron-recovery-test-only"
  }
}

run "empty_runtime_secret_contract" {
  command = plan

  assert {
    condition = nonsensitive(
      aws_secretsmanager_secret.runtime.name ==
      var.private_database_config.runtime_secret_name
    )
    error_message = "The runtime secret container must use the private configured name."
  }

  assert {
    condition     = issensitive(aws_secretsmanager_secret.runtime.name)
    error_message = "The runtime secret name must remain sensitive in plans."
  }

  assert {
    condition     = aws_secretsmanager_secret.runtime.recovery_window_in_days == 30
    error_message = "The runtime secret must retain a 30-day recovery window."
  }

  assert {
    condition = length([
      for block in split("resource \"", try(file("${path.module}/secrets.tf"), "")) : block
      if startswith(block, "aws_secretsmanager_secret\" \"runtime\" {") &&
      strcontains(block, "lifecycle {") &&
      strcontains(block, "prevent_destroy = true")
    ]) == 1
    error_message = "The runtime secret container must retain prevent_destroy."
  }

  assert {
    condition = alltrue([
      for forbidden in [
        "resource\\s+\"aws_secretsmanager_secret_version\"",
        "secret_string\\s*=",
        "secret_binary\\s*=",
        "GetSecretValue",
        "data\\s+\"aws_secretsmanager_",
        "output\\s+\"",
        ] : length(regexall(
          forbidden,
          join("\n", [
            for source in fileset(path.module, "*.tf") :
            file("${path.module}/${source}")
          ])
      )) == 0
    ])
    error_message = "Production Terraform must keep the runtime secret container empty and unread."
  }
}
