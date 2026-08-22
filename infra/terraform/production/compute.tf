locals {
  replacement_asset_sources = {
    "/opt/jobcron/compose.yaml" = {
      mode = "0644"
      path = "${path.module}/../../../deploy/production/compose.yaml"
    }
    "/opt/jobcron/Caddyfile" = {
      mode = "0644"
      path = "${path.module}/../../../deploy/production/Caddyfile"
    }
    "/opt/jobcron/jobcron-runtime.sh" = {
      mode = "0755"
      path = "${path.module}/../../../deploy/production/jobcron-runtime.sh"
    }
    "/etc/systemd/system/jobcron.service" = {
      mode = "0644"
      path = "${path.module}/../../../deploy/production/systemd/jobcron.service"
    }
    "/etc/systemd/system/jobcron-recovery.service" = {
      mode = "0644"
      path = "${path.module}/../../../deploy/production/systemd/jobcron-recovery.service"
    }
    "/etc/systemd/system/jobcron-recovery.timer" = {
      mode = "0644"
      path = "${path.module}/../../../deploy/production/systemd/jobcron-recovery.timer"
    }
  }

  replacement_assets = {
    for target, asset in local.replacement_asset_sources :
    target => {
      mode = asset.mode
      content = fileexists(asset.path) ? file(asset.path) : format(
        "# unavailable until the parallel Slice 4 task adds %s\n",
        basename(asset.path),
      )
    }
  }

  replacement_assets_ready = alltrue([
    for asset in values(local.replacement_asset_sources) :
    fileexists(asset.path)
  ])

  replacement_user_data = templatefile("${path.module}/templates/replacement-host.sh.tftpl", {
    assets = {
      for target, asset in local.replacement_assets :
      target => {
        mode    = asset.mode
        payload = base64gzip(asset.content)
        sha256  = sha256(asset.content)
      }
    }
    assets_ready       = local.replacement_assets_ready
    runtime_secret_arn = aws_secretsmanager_secret.runtime.arn
  })
}

resource "aws_iam_role" "replacement_host" {
  name = "jobcron-replacement-host"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = ""
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "replacement_host_ssm" {
  role       = aws_iam_role.replacement_host.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy" "replacement_host_runtime" {
  name = "jobcron-replacement-host-runtime"
  role = aws_iam_role.replacement_host.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "ReadRuntimeSecret"
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue"]
        Resource = [aws_secretsmanager_secret.runtime.arn]
      },
      {
        Sid      = "WriteRecoveryObjects"
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:AbortMultipartUpload"]
        Resource = ["${aws_s3_bucket.recovery.arn}/jobcron/*"]
      },
    ]
  })
}

resource "aws_iam_instance_profile" "replacement_host" {
  name = "jobcron-replacement-host"
  role = aws_iam_role.replacement_host.name
}

resource "aws_instance" "replacement_host" {
  ami                         = var.replacement_host_ami_id
  instance_type               = "t4g.micro"
  key_name                    = null
  subnet_id                   = aws_subnet.public[var.replacement_public_subnet_key].id
  associate_public_ip_address = true
  vpc_security_group_ids      = [aws_security_group.origin.id]
  iam_instance_profile        = aws_iam_instance_profile.replacement_host.name

  user_data = local.replacement_user_data

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
  }

  root_block_device {
    encrypted             = true
    volume_type           = "gp3"
    volume_size           = 8
    delete_on_termination = true
  }
}
