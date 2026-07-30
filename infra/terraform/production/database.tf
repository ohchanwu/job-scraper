resource "aws_subnet" "database" {
  for_each = toset(["database_a", "database_b"])

  vpc_id                  = aws_vpc.canonical.id
  availability_zone       = var.private_database_config.private_subnets[each.key].availability_zone
  cidr_block              = var.private_database_config.private_subnets[each.key].cidr_block
  map_public_ip_on_launch = false

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_route_table" "database" {
  vpc_id = aws_vpc.canonical.id

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_route_table_association" "database" {
  for_each = aws_subnet.database

  subnet_id      = each.value.id
  route_table_id = aws_route_table.database.id
}

resource "aws_security_group" "origin" {
  vpc_id = aws_vpc.canonical.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    "jobcron:edge-target" = "origin-security-group"
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_security_group" "database" {
  vpc_id = aws_vpc.canonical.id
  tags   = {}

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "database_postgresql_from_origin" {
  security_group_id            = aws_security_group.database.id
  referenced_security_group_id = aws_security_group.origin.id
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_db_subnet_group" "production" {
  subnet_ids = values(aws_subnet.database)[*].id
}

resource "aws_db_parameter_group" "production" {
  family = "postgres18"

  parameter {
    name         = "rds.force_ssl"
    value        = "1"
    apply_method = "pending-reboot"
  }
}

resource "aws_db_instance" "production" {
  identifier                = var.private_database_config.database_identifier
  db_name                   = var.private_database_config.database_name
  username                  = var.private_database_config.master_username
  final_snapshot_identifier = var.private_database_config.final_snapshot_identifier

  engine         = "postgres"
  engine_version = "18.4"
  instance_class = "db.t4g.micro"

  allocated_storage = 20
  storage_type      = "gp3"
  storage_encrypted = true

  multi_az            = false
  publicly_accessible = false
  port                = 5432

  backup_retention_period = 7
  backup_window           = "18:00-18:30"
  maintenance_window      = "sun:19:00-sun:19:30"

  auto_minor_version_upgrade  = true
  deletion_protection         = true
  manage_master_user_password = true
  copy_tags_to_snapshot       = true
  skip_final_snapshot         = false

  db_subnet_group_name   = aws_db_subnet_group.production.name
  parameter_group_name   = aws_db_parameter_group.production.name
  vpc_security_group_ids = [aws_security_group.database.id]

  lifecycle {
    prevent_destroy = true
  }
}
