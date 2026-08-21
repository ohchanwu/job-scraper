mock_provider "aws" {}

override_resource {
  target          = aws_route_table.public
  override_during = plan
  values = {
    id = "rtb-jobcron-test-only"
  }
}

override_resource {
  target          = aws_s3_bucket.recovery
  override_during = plan
  values = {
    arn = "arn:aws:s3:::jobcron-recovery-test-only"
  }
}

variables {
  replacement_host_ami_id = "ami-0123456789abcdef0"

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

run "protected_recovery_bucket_contract" {
  command = plan

  assert {
    condition = nonsensitive(
      aws_s3_bucket.recovery.bucket ==
      var.private_database_config.recovery_bucket_name
    )
    error_message = "The recovery bucket must use the private configured name."
  }

  assert {
    condition     = issensitive(aws_s3_bucket.recovery.bucket)
    error_message = "The recovery bucket name must remain sensitive in plans."
  }

  assert {
    condition = (
      aws_s3_bucket_public_access_block.recovery.block_public_acls &&
      aws_s3_bucket_public_access_block.recovery.block_public_policy &&
      aws_s3_bucket_public_access_block.recovery.ignore_public_acls &&
      aws_s3_bucket_public_access_block.recovery.restrict_public_buckets
    )
    error_message = "The recovery bucket must enable all four public-access blocks."
  }

  assert {
    condition     = aws_s3_bucket_versioning.recovery.versioning_configuration[0].status == "Enabled"
    error_message = "The recovery bucket must enable versioning."
  }

  assert {
    condition     = one(aws_s3_bucket_server_side_encryption_configuration.recovery.rule).apply_server_side_encryption_by_default[0].sse_algorithm == "AES256"
    error_message = "The recovery bucket must enable AES256 default encryption."
  }

  assert {
    condition = jsonencode(jsondecode(aws_s3_bucket_policy.recovery.policy)) == jsonencode({
      Version = "2012-10-17"
      Statement = [{
        Sid       = "DenyInsecureTransport"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource = [
          "arn:aws:s3:::jobcron-recovery-test-only",
          "arn:aws:s3:::jobcron-recovery-test-only/*",
        ]
        Condition = {
          Bool = {
            "aws:SecureTransport" = "false"
          }
        }
      }]
    })
    error_message = "The recovery bucket policy must deny every insecure S3 action on the bucket and objects."
  }

  assert {
    condition = length(regexall(
      "data\\s+\"aws_iam_policy_document\"\\s+\"recovery_bucket\"",
      try(file("${path.module}/recovery.tf"), "")
    )) == 0
    error_message = "The recovery policy must remain inline so Slice 3 plans contain no deferred policy-document read."
  }

  assert {
    condition = (
      length(aws_s3_bucket_lifecycle_configuration.recovery.rule) == 2 &&
      alltrue([
        for rule in aws_s3_bucket_lifecycle_configuration.recovery.rule :
        rule.status == "Enabled"
      ])
    )
    error_message = "The recovery bucket must retain exactly two enabled lifecycle rules."
  }

  assert {
    condition = (
      one([
        for rule in aws_s3_bucket_lifecycle_configuration.recovery.rule : rule
        if rule.id == "expire-verified-after-off-cloud-copy"
      ]).expiration[0].days == 14 &&
      try(one([
        for rule in aws_s3_bucket_lifecycle_configuration.recovery.rule : rule
        if rule.id == "expire-verified-after-off-cloud-copy"
      ]).noncurrent_version_expiration[0].noncurrent_days, 0) == 1 &&
      one(one([
        for rule in aws_s3_bucket_lifecycle_configuration.recovery.rule : rule
        if rule.id == "expire-verified-after-off-cloud-copy"
      ]).filter).tag[0].key == "macbook-copy" &&
      one(one([
        for rule in aws_s3_bucket_lifecycle_configuration.recovery.rule : rule
        if rule.id == "expire-verified-after-off-cloud-copy"
      ]).filter).tag[0].value == "verified"
    )
    error_message = "MacBook-verified recovery objects must expire after 14 days."
  }

  assert {
    condition = (
      one([
        for rule in aws_s3_bucket_lifecycle_configuration.recovery.rule : rule
        if rule.id == "expire-all-objects"
      ]).expiration[0].days == 90 &&
      try(one([
        for rule in aws_s3_bucket_lifecycle_configuration.recovery.rule : rule
        if rule.id == "expire-all-objects"
      ]).noncurrent_version_expiration[0].noncurrent_days, 0) == 1 &&
      length(one([
        for rule in aws_s3_bucket_lifecycle_configuration.recovery.rule : rule
        if rule.id == "expire-all-objects"
      ]).filter) == 1 &&
      length(one(one([
        for rule in aws_s3_bucket_lifecycle_configuration.recovery.rule : rule
        if rule.id == "expire-all-objects"
      ]).filter).tag) == 0
    )
    error_message = "Every recovery object must expire after 90 days through an empty filter."
  }

  assert {
    condition = alltrue([
      for declaration in [
        "aws_s3_bucket\" \"recovery\" {",
        "aws_s3_bucket_versioning\" \"recovery\" {",
        "aws_s3_bucket_server_side_encryption_configuration\" \"recovery\" {",
        "aws_s3_bucket_policy\" \"recovery\" {",
        "aws_s3_bucket_lifecycle_configuration\" \"recovery\" {",
        ] : length([
          for block in split("resource \"", try(file("${path.module}/recovery.tf"), "")) : block
          if startswith(block, declaration) &&
          strcontains(block, "lifecycle {") &&
          strcontains(block, "prevent_destroy = true")
      ]) == 1
    ])
    error_message = "All five protected recovery resources must retain prevent_destroy."
  }

  assert {
    condition = alltrue([
      for forbidden in [
        "resource\\s+\"aws_s3_bucket_acl\"",
        "resource\\s+\"aws_s3_bucket_website",
        "resource\\s+\"aws_s3_object\"",
        "resource\\s+\"aws_s3_bucket_object\"",
        "\\bacl\\s*=",
        "\\bwebsite\\s*\\{",
        "\\btransition\\s*\\{",
        "noncurrent_version_transition\\s*\\{",
        "newer_noncurrent_versions",
        "expired_object_delete_marker",
        "effect\\s*=\\s*\"Allow\"",
        "output\\s+\"",
        ] : length(regexall(
          forbidden,
          try(file("${path.module}/recovery.tf"), "")
      )) == 0
    ])
    error_message = "The recovery bucket must not expose or add unreviewed storage behavior."
  }

  assert {
    condition = strcontains(
      try(file("${path.module}/recovery.tf"), ""),
      "depends_on = [aws_s3_bucket_versioning.recovery]"
    )
    error_message = "Recovery lifecycle configuration must wait for enabled versioning."
  }
}
