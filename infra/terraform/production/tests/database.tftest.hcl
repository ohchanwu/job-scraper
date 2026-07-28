mock_provider "aws" {}

override_resource {
  target          = aws_vpc.canonical
  override_during = plan
  values = {
    id = "vpc-jobcron-test-only"
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
  target          = aws_subnet.database["database_a"]
  override_during = plan
  values = {
    id = "subnet-database-a-test-only"
  }
}

override_resource {
  target          = aws_subnet.database["database_b"]
  override_during = plan
  values = {
    id = "subnet-database-b-test-only"
  }
}

override_resource {
  target          = aws_route_table.database
  override_during = plan
  values = {
    id    = "rtb-database-test-only"
    route = []
  }
}

override_resource {
  target          = aws_security_group.origin
  override_during = plan
  values = {
    id      = "sg-origin-test-only"
    ingress = []
  }
}

override_resource {
  target          = aws_security_group.database
  override_during = plan
  values = {
    id      = "sg-database-test-only"
    ingress = []
  }
}

override_resource {
  target          = aws_db_subnet_group.production
  override_during = plan
  values = {
    name = "db-subnet-group-test-only"
  }
}

override_resource {
  target          = aws_db_parameter_group.production
  override_during = plan
  values = {
    name = "db-parameter-group-test-only"
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

run "private_database_network_contract" {
  command = plan

  assert {
    condition = !strcontains(
      file("${path.module}/database.tf"),
      "nonsensitive(var.private_database_config"
    )
    error_message = "Production database resources must not declassify private database configuration."
  }

  assert {
    condition = length(regexall(
      "resource\\s+\"aws_route\"\\s+\"",
      file("${path.module}/database.tf")
    )) == 0
    error_message = "The private database network must not declare an aws_route resource."
  }

  assert {
    condition = alltrue([
      for declaration in [
        "aws_subnet\" \"database\" {",
        "aws_route_table\" \"database\" {",
        "aws_security_group\" \"origin\" {",
        "aws_security_group\" \"database\" {",
        "aws_vpc_security_group_ingress_rule\" \"database_postgresql_from_origin\" {",
        "aws_db_instance\" \"production\" {",
        ] : length([
          for block in split("resource \"", file("${path.module}/database.tf")) : block
          if startswith(block, declaration) &&
          strcontains(block, "lifecycle {") &&
          strcontains(block, "prevent_destroy = true")
      ]) == 1
    ])
    error_message = "All six protected database resources must retain prevent_destroy."
  }

  assert {
    condition     = toset(keys(aws_subnet.database)) == toset(["database_a", "database_b"])
    error_message = "The database subnet addresses must remain stable."
  }

  assert {
    condition = alltrue([
      for subnet in values(aws_subnet.database) :
      subnet.vpc_id == aws_vpc.canonical.id &&
      subnet.map_public_ip_on_launch == false
    ])
    error_message = "Database subnets must remain private members of the canonical VPC."
  }

  assert {
    condition = (
      length(distinct(values(aws_subnet.database)[*].availability_zone)) == 2 &&
      length(distinct(values(aws_subnet.database)[*].cidr_block)) == 2
    )
    error_message = "Database subnets must use two distinct AZs and CIDRs."
  }

  assert {
    condition = alltrue([
      for subnet in values(aws_subnet.database) :
      issensitive(subnet.availability_zone) &&
      issensitive(subnet.cidr_block)
    ])
    error_message = "Database subnet AZs and CIDRs must remain sensitive in plans."
  }

  assert {
    condition = (
      aws_route_table.database.vpc_id == aws_vpc.canonical.id &&
      length(aws_route_table.database.route) == 0
    )
    error_message = "The database route table must contain only the implicit local route."
  }

  assert {
    condition     = toset(keys(aws_route_table_association.database)) == toset(["database_a", "database_b"])
    error_message = "Each database subnet must use the local-only database route table."
  }

  assert {
    condition = alltrue([
      for key, association in aws_route_table_association.database :
      association.subnet_id == aws_subnet.database[key].id &&
      association.route_table_id == aws_route_table.database.id
    ])
    error_message = "Database route-table associations must bind the exact private subnets."
  }

  assert {
    condition = (
      aws_security_group.origin.vpc_id == aws_vpc.canonical.id &&
      length(aws_security_group.origin.ingress) == 0
    )
    error_message = "The origin security group must not expose an inbound rule."
  }

  assert {
    condition = (
      length(aws_security_group.origin.egress) == 1 &&
      one(aws_security_group.origin.egress).protocol == "-1" &&
      one(aws_security_group.origin.egress).from_port == 0 &&
      one(aws_security_group.origin.egress).to_port == 0 &&
      toset(one(aws_security_group.origin.egress).cidr_blocks) == toset(["0.0.0.0/0"])
    )
    error_message = "The origin security group must retain normal outbound egress."
  }

  assert {
    condition = aws_security_group.origin.tags == tomap({
      "jobcron:edge-target" = "origin-security-group"
    })
    error_message = "The origin security group must retain its deterministic discovery tag."
  }

  assert {
    condition = (
      aws_security_group.database.vpc_id == aws_vpc.canonical.id &&
      length(aws_security_group.database.ingress) == 0
    )
    error_message = "The database security group must use only standalone ingress rules."
  }

  assert {
    condition     = length(aws_security_group.database.tags) == 0
    error_message = "The database security group must not inherit the origin discovery tag."
  }

  assert {
    condition     = try(aws_vpc.canonical.tags["jobcron:edge-target"], null) == null
    error_message = "Window 1 must not add the discovery tag to the adopted VPC."
  }

  assert {
    condition = (
      aws_vpc_security_group_ingress_rule.database_postgresql_from_origin.ip_protocol == "tcp" &&
      aws_vpc_security_group_ingress_rule.database_postgresql_from_origin.from_port == 5432 &&
      aws_vpc_security_group_ingress_rule.database_postgresql_from_origin.to_port == 5432 &&
      aws_vpc_security_group_ingress_rule.database_postgresql_from_origin.security_group_id ==
      aws_security_group.database.id &&
      aws_vpc_security_group_ingress_rule.database_postgresql_from_origin.referenced_security_group_id ==
      aws_security_group.origin.id
    )
    error_message = "PostgreSQL ingress must be the exact origin-SG-to-database-SG edge."
  }
}

run "postgres_contract" {
  command = plan

  assert {
    condition = (
      issensitive(aws_db_instance.production.identifier) &&
      issensitive(aws_db_instance.production.db_name) &&
      issensitive(aws_db_instance.production.username) &&
      issensitive(aws_db_instance.production.final_snapshot_identifier)
    )
    error_message = "Private RDS names must remain sensitive in plans."
  }

  assert {
    condition     = toset(aws_db_subnet_group.production.subnet_ids) == toset(values(aws_subnet.database)[*].id)
    error_message = "The DB subnet group must contain exactly the two private database subnets."
  }

  assert {
    condition = (
      aws_db_parameter_group.production.family == "postgres18" &&
      length(aws_db_parameter_group.production.parameter) == 1 &&
      one(aws_db_parameter_group.production.parameter).name == "rds.force_ssl" &&
      one(aws_db_parameter_group.production.parameter).value == "1" &&
      one(aws_db_parameter_group.production.parameter).apply_method == "immediate"
    )
    error_message = "The PostgreSQL 18 parameter group must enforce SSL."
  }

  assert {
    condition = (
      aws_db_instance.production.engine == "postgres" &&
      aws_db_instance.production.engine_version == "18.4" &&
      aws_db_instance.production.instance_class == "db.t4g.micro" &&
      aws_db_instance.production.allocated_storage == 20 &&
      aws_db_instance.production.storage_type == "gp3" &&
      aws_db_instance.production.storage_encrypted == true &&
      aws_db_instance.production.multi_az == false &&
      aws_db_instance.production.publicly_accessible == false &&
      aws_db_instance.production.port == 5432
    )
    error_message = "The production database must retain the locked PostgreSQL 18.4 shape."
  }

  assert {
    condition = (
      aws_db_instance.production.backup_retention_period == 7 &&
      aws_db_instance.production.backup_window == "18:00-18:30" &&
      aws_db_instance.production.maintenance_window == "sun:19:00-sun:19:30" &&
      aws_db_instance.production.auto_minor_version_upgrade == true &&
      aws_db_instance.production.deletion_protection == true &&
      aws_db_instance.production.manage_master_user_password == true &&
      aws_db_instance.production.copy_tags_to_snapshot == true &&
      aws_db_instance.production.skip_final_snapshot == false
    )
    error_message = "The production database safety and backup controls must remain enabled."
  }

  assert {
    condition = (
      aws_db_instance.production.db_subnet_group_name == aws_db_subnet_group.production.name &&
      aws_db_instance.production.parameter_group_name == aws_db_parameter_group.production.name &&
      aws_db_instance.production.vpc_security_group_ids == toset([aws_security_group.database.id])
    )
    error_message = "The production database must use only the private database network controls."
  }
}

run "reject_invalid_private_database_config" {
  command = plan

  variables {
    private_database_config = {
      private_subnets = {
        database_a = {
          availability_zone = "example-1a"
          cidr_block        = "10.255.0.64/28"
        }
        database_c = {
          availability_zone = "example-1a"
          cidr_block        = "10.255.0.64/28"
        }
      }
      database_identifier       = ""
      database_name             = "Invalid-Database"
      master_username           = "Invalid-User"
      final_snapshot_identifier = ""
      runtime_secret_name       = ""
      recovery_bucket_name      = ""
    }
  }

  expect_failures = [var.private_database_config]
}
