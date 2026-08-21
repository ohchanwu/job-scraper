mock_provider "aws" {}

override_resource {
  target          = aws_secretsmanager_secret.runtime
  override_during = plan
  values = {
    arn = "arn:aws:secretsmanager:example-1:000000000000:secret:test-only"
  }
}

override_resource {
  target          = aws_s3_bucket.recovery
  override_during = plan
  values = {
    arn = "arn:aws:s3:::test-only-recovery"
  }
}

override_resource {
  target          = aws_route_table.public
  override_during = plan
  values = {
    id = "rtb-jobcron-test-only"
  }
}

override_resource {
  target          = aws_subnet.public
  override_during = plan
  values = {
    id = "subnet-jobcron-test-only"
  }
}

override_resource {
  target          = aws_security_group.origin
  override_during = plan
  values = {
    id = "sg-jobcron-test-only"
  }
}

override_resource {
  target          = aws_instance.replacement_host
  override_during = plan
  values = {
    id = "i-jobcron-test-only"
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

  replacement_host_ami_id       = "ami-0123456789abcdef0"
  replacement_public_subnet_key = "public_a"
}

run "replacement_host_contract" {
  command = plan

  assert {
    condition     = aws_instance.replacement_host.ami == var.replacement_host_ami_id
    error_message = "The replacement host must use the explicitly pinned AMI."
  }

  assert {
    condition = (
      aws_instance.replacement_host.instance_type == "t4g.micro" &&
      aws_instance.replacement_host.associate_public_ip_address == true &&
      aws_instance.replacement_host.subnet_id ==
      aws_subnet.public[var.replacement_public_subnet_key].id &&
      toset(aws_instance.replacement_host.vpc_security_group_ids) ==
      toset([aws_security_group.origin.id])
    )
    error_message = "The replacement host must use one canonical public subnet, ephemeral IPv4, no key, and only the origin group."
  }

  assert {
    condition = length(regexall(
      "key_name\\s+=\\s+null",
      file("${path.module}/compute.tf")
    )) == 1
    error_message = "The replacement host must explicitly disable EC2 key-pair access."
  }

  assert {
    condition = (
      one(aws_instance.replacement_host.metadata_options).http_endpoint == "enabled" &&
      one(aws_instance.replacement_host.metadata_options).http_tokens == "required" &&
      one(aws_instance.replacement_host.metadata_options).http_put_response_hop_limit == 1 &&
      one(aws_instance.replacement_host.root_block_device).encrypted == true &&
      one(aws_instance.replacement_host.root_block_device).volume_type == "gp3" &&
      one(aws_instance.replacement_host.root_block_device).volume_size == 8 &&
      one(aws_instance.replacement_host.root_block_device).delete_on_termination == true
    )
    error_message = "The replacement host must require IMDSv2 and an encrypted disposable 8 GiB gp3 root."
  }

  assert {
    condition = (
      aws_vpc_security_group_ingress_rule.database_postgresql_from_origin.from_port == 5432 &&
      aws_vpc_security_group_ingress_rule.database_postgresql_from_origin.to_port == 5432 &&
      aws_vpc_security_group_ingress_rule.database_postgresql_from_origin.ip_protocol == "tcp" &&
      aws_vpc_security_group_ingress_rule.database_postgresql_from_origin.referenced_security_group_id ==
      aws_security_group.origin.id
    )
    error_message = "The existing origin-to-database PostgreSQL rule must remain unchanged."
  }

  assert {
    condition = alltrue([
      for forbidden in [
        "resource\\s+\"aws_security_group\"",
        "resource\\s+\"aws_vpc_security_group_(egress|ingress)_rule\"",
        "resource\\s+\"aws_eip_association\"",
        "resource\\s+\"aws_key_pair\"",
        "aws_eip\\.origin",
        ] : length(regexall(
          forbidden,
          file("${path.module}/compute.tf")
      )) == 0
    ])
    error_message = "Slice 4 must not duplicate network policy, associate an EIP, or create a key pair."
  }

  assert {
    condition = (
      jsondecode(aws_iam_role.replacement_host.assume_role_policy).Statement == [{
        Action    = "sts:AssumeRole"
        Effect    = "Allow"
        Principal = { Service = "ec2.amazonaws.com" }
        Sid       = ""
      }] &&
      aws_iam_role_policy_attachment.replacement_host_ssm.policy_arn ==
      "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
    )
    error_message = "The replacement role must trust only EC2 and attach only the SSM core managed policy."
  }

  assert {
    condition = (
      toset(flatten([
        for statement in jsondecode(aws_iam_role_policy.replacement_host_runtime.policy).Statement :
        statement.Action
        ])) == toset([
        "secretsmanager:GetSecretValue",
        "s3:PutObject",
        "s3:AbortMultipartUpload",
      ]) &&
      toset(flatten([
        for statement in jsondecode(aws_iam_role_policy.replacement_host_runtime.policy).Statement :
        statement.Resource
        ])) == toset([
        aws_secretsmanager_secret.runtime.arn,
        "${aws_s3_bucket.recovery.arn}/jobcron/*",
      ])
    )
    error_message = "Runtime IAM must read only the runtime secret and write only recovery objects."
  }

  assert {
    condition = alltrue([
      for forbidden in [
        "rds:",
        "secretsmanager:(Put|Update|Delete)",
        "s3:(List|Get|Delete)",
        "\"Action\":\"\\*\"",
        "\"Resource\":\"\\*\"",
        ] : length(regexall(
          forbidden,
          aws_iam_role_policy.replacement_host_runtime.policy
      )) == 0
    ])
    error_message = "Runtime IAM must not gain database, secret mutation, recovery read/delete, or wildcard access."
  }

  assert {
    condition = (
      length(aws_instance.replacement_host.user_data) < 16384 &&
      strcontains(aws_instance.replacement_host.user_data, "dnf install -y") &&
      strcontains(aws_instance.replacement_host.user_data, "docker-compose-plugin") &&
      strcontains(aws_instance.replacement_host.user_data, "awscli2") &&
      strcontains(aws_instance.replacement_host.user_data, " jq ") &&
      strcontains(aws_instance.replacement_host.user_data, "postgresql15") &&
      !strcontains(aws_instance.replacement_host.user_data, "/opt/jobcron/.env") &&
      strcontains(aws_instance.replacement_host.user_data, "/etc/jobcron/runtime-secret-id") &&
      strcontains(aws_instance.replacement_host.user_data, "systemctl enable") &&
      strcontains(aws_instance.replacement_host.user_data, "systemctl stop jobcron.service")
    )
    error_message = "Bootstrap must stay below EC2 raw user-data limits, install tools, copy assets, enable units, and leave Jobcron stopped."
  }

  assert {
    condition = local.replacement_assets_ready || length(regexall(
      "(?s)required deployment asset missing.*exit 1.*dnf install",
      aws_instance.replacement_host.user_data
    )) == 1
    error_message = "An incomplete parallel-task integration must abort before host bootstrap writes anything."
  }

  assert {
    condition = (
      length(regexall(
        "arn:aws:secretsmanager:example-1:000000000000:secret:test-only",
        aws_instance.replacement_host.user_data
      )) == 1 &&
      length(regexall(
        "(?i)(registry-token|secret-value|database[_-]?url|image[_-]?digest)",
        aws_instance.replacement_host.user_data
      )) == 0
    )
    error_message = "Bootstrap may persist only the approved runtime-secret identifier, never private values."
  }

  assert {
    condition = (
      output.replacement_instance_id == aws_instance.replacement_host.id &&
      issensitive(output.replacement_instance_id) &&
      length(regexall(
        "output\\s+\"",
        join("\n", [
          for source in fileset(path.module, "*.tf") :
          file("${path.module}/${source}")
        ])
      )) == 1
    )
    error_message = "The only Terraform output must be the sensitive replacement instance selector."
  }
}

run "rejects_unpinned_replacement_host_ami" {
  command = plan

  variables {
    replacement_host_ami_id = "ami-latest"
  }

  expect_failures = [var.replacement_host_ami_id]
}
