import {
  to = aws_vpc.canonical
  id = var.canonical_import_ids.vpc
}

import {
  to = aws_internet_gateway.canonical
  id = var.canonical_import_ids.internet_gateway
}

import {
  to = aws_subnet.public["public_a"]
  id = var.canonical_import_ids.public_subnets["public_a"]
}

import {
  to = aws_subnet.public["public_b"]
  id = var.canonical_import_ids.public_subnets["public_b"]
}

import {
  to = aws_subnet.public["public_c"]
  id = var.canonical_import_ids.public_subnets["public_c"]
}

import {
  to = aws_subnet.public["public_d"]
  id = var.canonical_import_ids.public_subnets["public_d"]
}

import {
  to = aws_route_table.public
  id = var.canonical_import_ids.public_route_table
}

import {
  to = aws_route.public_ipv4_default
  id = var.canonical_import_ids.public_ipv4_default
}
