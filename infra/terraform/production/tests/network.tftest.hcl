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
}

run "network_contract" {
  command = plan

  assert {
    condition     = length(aws_subnet.public) == 4
    error_message = "Canonical production networking must retain four public subnets."
  }

  assert {
    condition = (
      toset(keys(aws_subnet.public)) ==
      toset(["public_a", "public_b", "public_c", "public_d"])
    )
    error_message = "Canonical public subnet addresses must remain stable."
  }

  assert {
    condition     = aws_route.public_ipv4_default.destination_cidr_block == "0.0.0.0/0"
    error_message = "The public route must remain the IPv4 default route."
  }

  assert {
    condition     = aws_eip.origin.domain == "vpc"
    error_message = "The adopted origin EIP must remain VPC-scoped."
  }

}

run "reject_missing_public_subnet" {
  command = plan

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
      }
    }
  }

  expect_failures = [var.canonical_network_config]
}
