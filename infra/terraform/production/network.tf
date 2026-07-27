resource "aws_vpc" "canonical" {
  cidr_block                           = var.canonical_network_config.vpc.cidr_block
  enable_dns_hostnames                 = var.canonical_network_config.vpc.enable_dns_hostnames
  enable_dns_support                   = var.canonical_network_config.vpc.enable_dns_support
  enable_network_address_usage_metrics = var.canonical_network_config.vpc.enable_network_address_usage_metrics
  instance_tenancy                     = var.canonical_network_config.vpc.instance_tenancy

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_internet_gateway" "canonical" {
  vpc_id = aws_vpc.canonical.id

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_subnet" "public" {
  for_each = toset(["public_a", "public_b", "public_c", "public_d"])

  vpc_id                                         = aws_vpc.canonical.id
  availability_zone                              = var.canonical_network_config.public_subnets[each.key].availability_zone
  cidr_block                                     = var.canonical_network_config.public_subnets[each.key].cidr_block
  assign_ipv6_address_on_creation                = var.canonical_network_config.public_subnets[each.key].assign_ipv6_address_on_creation
  enable_dns64                                   = var.canonical_network_config.public_subnets[each.key].enable_dns64
  enable_resource_name_dns_a_record_on_launch    = var.canonical_network_config.public_subnets[each.key].enable_resource_name_dns_a_record_on_launch
  enable_resource_name_dns_aaaa_record_on_launch = var.canonical_network_config.public_subnets[each.key].enable_resource_name_dns_aaaa_record_on_launch
  map_public_ip_on_launch                        = var.canonical_network_config.public_subnets[each.key].map_public_ip_on_launch
  private_dns_hostname_type_on_launch            = var.canonical_network_config.public_subnets[each.key].private_dns_hostname_type_on_launch

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.canonical.id

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_route" "public_ipv4_default" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.canonical.id

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_eip" "origin" {
  domain = "vpc"

  lifecycle {
    prevent_destroy = true
  }
}
