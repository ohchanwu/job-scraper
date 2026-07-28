mock_provider "aws" {}

override_data {
  target          = data.aws_security_group.origin
  override_during = plan
  values = {
    id     = "sg-0123456789abcdef0"
    vpc_id = "vpc-0123456789abcdef0"
  }
}

override_resource {
  target          = aws_ec2_managed_prefix_list.cloudflare_ipv4
  override_during = plan
  values = {
    id = "pl-0123456789abcdef0"
  }
}

run "cloudflare_edge_contract" {
  command = plan

  variables {
    cloudflare_ipv4_cidrs = [
      "192.0.2.0/28",
      "192.0.2.16/28",
      "192.0.2.32/28",
      "192.0.2.48/28",
      "192.0.2.64/28",
      "192.0.2.80/28",
      "192.0.2.96/28",
      "192.0.2.112/28",
      "192.0.2.128/28",
      "192.0.2.144/28",
    ]
  }

  assert {
    condition = data.aws_security_group.origin.tags == tomap({
      "jobcron:edge-target" = "origin-security-group"
    })
    error_message = "Origin discovery must use only the Slice 3 semantic tag."
  }

  assert {
    condition = (
      aws_ec2_managed_prefix_list.cloudflare_ipv4.address_family == "IPv4" &&
      aws_ec2_managed_prefix_list.cloudflare_ipv4.max_entries == 20 &&
      toset([
        for entry in aws_ec2_managed_prefix_list.cloudflare_ipv4.entry :
        entry.cidr
      ]) == toset(var.cloudflare_ipv4_cidrs) &&
      aws_ec2_managed_prefix_list.cloudflare_ipv4.tags == tomap({
        "jobcron:edge-source" = "cloudflare-ipv4"
      })
    )
    error_message = "The managed prefix list must contain exactly the validated IPv4 input."
  }

  assert {
    condition = (
      aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare.security_group_id ==
      data.aws_security_group.origin.id &&
      aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare.prefix_list_id ==
      aws_ec2_managed_prefix_list.cloudflare_ipv4.id &&
      aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare.ip_protocol ==
      "tcp" &&
      aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare.from_port == 443 &&
      aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare.to_port == 443 &&
      aws_vpc_security_group_ingress_rule.origin_https_from_cloudflare.tags == tomap({
        "jobcron:edge-rule" = "origin-https-from-cloudflare"
      })
    )
    error_message = "The origin rule must allow only prefix-list-backed TCP 443 ingress."
  }
}

run "rejects_nine_entries" {
  command = plan

  variables {
    cloudflare_ipv4_cidrs = [
      "192.0.2.0/28",
      "192.0.2.16/28",
      "192.0.2.32/28",
      "192.0.2.48/28",
      "192.0.2.64/28",
      "192.0.2.80/28",
      "192.0.2.96/28",
      "192.0.2.112/28",
      "192.0.2.128/28",
    ]
  }

  expect_failures = [var.cloudflare_ipv4_cidrs]
}

run "rejects_twenty_one_entries" {
  command = plan

  variables {
    cloudflare_ipv4_cidrs = [
      for index in range(21) : "192.0.2.${index}/32"
    ]
  }

  expect_failures = [var.cloudflare_ipv4_cidrs]
}

run "rejects_duplicate_entries" {
  command = plan

  variables {
    cloudflare_ipv4_cidrs = [
      "192.0.2.0/28",
      "192.0.2.16/28",
      "192.0.2.32/28",
      "192.0.2.48/28",
      "192.0.2.64/28",
      "192.0.2.80/28",
      "192.0.2.96/28",
      "192.0.2.112/28",
      "192.0.2.128/28",
      "192.0.2.128/28",
    ]
  }

  expect_failures = [var.cloudflare_ipv4_cidrs]
}
