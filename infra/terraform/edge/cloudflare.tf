data "aws_security_group" "origin" {
  tags = {
    "jobcron:edge-target" = "origin-security-group"
  }
}

resource "aws_ec2_managed_prefix_list" "cloudflare_ipv4" {
  name           = "jobcron-cloudflare-ipv4"
  address_family = "IPv4"
  max_entries    = 20

  dynamic "entry" {
    for_each = var.cloudflare_ipv4_cidrs
    content {
      cidr = entry.value
    }
  }

  tags = {
    "jobcron:edge-source" = "cloudflare-ipv4"
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "origin_https_from_cloudflare" {
  security_group_id = data.aws_security_group.origin.id
  prefix_list_id    = aws_ec2_managed_prefix_list.cloudflare_ipv4.id
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443

  tags = {
    "jobcron:edge-rule" = "origin-https-from-cloudflare"
  }

  lifecycle {
    prevent_destroy = true
  }
}
