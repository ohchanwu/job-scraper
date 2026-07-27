mock_provider "aws" {}

override_resource {
  target          = aws_s3_bucket.state
  override_during = plan
  values = {
    arn = "arn:aws:s3:::jobcron-state-test-only"
  }
}

override_resource {
  target          = aws_iam_openid_connect_provider.github
  override_during = plan
  values = {
    arn = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
  }
}

override_data {
  target          = data.aws_iam_policy_document.production_assume
  override_during = plan
  values = {
    json = jsonencode({
      Version = "2012-10-17"
      Statement = [{
        Sid       = "Production"
        Effect    = "Allow"
        Action    = "sts:AssumeRoleWithWebIdentity"
        Principal = { Federated = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com" }
      }]
    })
  }
}

override_data {
  target          = data.aws_iam_policy_document.edge_assume
  override_during = plan
  values = {
    json = jsonencode({
      Version = "2012-10-17"
      Statement = [{
        Sid       = "Edge"
        Effect    = "Allow"
        Action    = "sts:AssumeRoleWithWebIdentity"
        Principal = { Federated = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com" }
      }]
    })
  }
}

override_data {
  target          = data.aws_iam_policy_document.production_state
  override_during = plan
  values = {
    json = jsonencode({
      Version = "2012-10-17"
      Statement = [{
        Sid      = "Production"
        Effect   = "Allow"
        Action   = "s3:GetObject"
        Resource = "*"
      }]
    })
  }
}

override_data {
  target          = data.aws_iam_policy_document.production_network_read
  override_during = plan
  values = {
    json = jsonencode({
      Version = "2012-10-17"
      Statement = [{
        Sid      = "ProductionNetworkRead"
        Effect   = "Allow"
        Action   = "ec2:DescribeVpcs"
        Resource = "*"
      }]
    })
  }
}

override_data {
  target          = data.aws_iam_policy_document.edge_state
  override_during = plan
  values = {
    json = jsonencode({
      Version = "2012-10-17"
      Statement = [{
        Sid      = "Edge"
        Effect   = "Allow"
        Action   = "s3:GetObject"
        Resource = "*"
      }]
    })
  }
}

override_resource {
  target          = aws_iam_policy.production_state
  override_during = plan
  values = {
    arn = "arn:aws:iam::123456789012:policy/JobcronTerraformProductionState"
  }
}

override_resource {
  target          = aws_iam_policy.production_network_read
  override_during = plan
  values = {
    arn = "arn:aws:iam::123456789012:policy/JobcronTerraformProductionNetworkRead"
  }
}

override_resource {
  target          = aws_iam_policy.edge_state
  override_during = plan
  values = {
    arn = "arn:aws:iam::123456789012:policy/JobcronTerraformEdgeState"
  }
}

variables {
  state_bucket_name = "jobcron-state-test-only"
}

run "identity_contract" {
  command = plan

  assert {
    condition = (
      length(data.aws_iam_policy_document.production_assume.statement) == 1 &&
      one(data.aws_iam_policy_document.production_assume.statement).actions ==
      toset(["sts:AssumeRoleWithWebIdentity"]) &&
      length(one(data.aws_iam_policy_document.production_assume.statement).principals) == 1 &&
      one(one(data.aws_iam_policy_document.production_assume.statement).principals).type ==
      "Federated" &&
      one(one(data.aws_iam_policy_document.production_assume.statement).principals).identifiers ==
      toset([aws_iam_openid_connect_provider.github.arn]) &&
      length(one(data.aws_iam_policy_document.production_assume.statement).condition) == 2 &&
      alltrue([
        for condition in one(data.aws_iam_policy_document.production_assume.statement).condition :
        condition.test == "StringEquals" && (
          condition.variable == "token.actions.githubusercontent.com:aud"
          ? (
            length(condition.values) == 1 &&
            one(condition.values) == "sts.amazonaws.com"
          )
          : (
            condition.variable == "token.actions.githubusercontent.com:sub" &&
            length(condition.values) == 1 &&
            one(condition.values) == "repo:ohchanwu/jobcron:environment:production"
          )
        )
      ])
    )
    error_message = "Production trust must require the GitHub audience and production environment."
  }

  assert {
    condition = (
      length(data.aws_iam_policy_document.edge_assume.statement) == 1 &&
      one(data.aws_iam_policy_document.edge_assume.statement).actions ==
      toset(["sts:AssumeRoleWithWebIdentity"]) &&
      length(one(data.aws_iam_policy_document.edge_assume.statement).principals) == 1 &&
      one(one(data.aws_iam_policy_document.edge_assume.statement).principals).type ==
      "Federated" &&
      one(one(data.aws_iam_policy_document.edge_assume.statement).principals).identifiers ==
      toset([aws_iam_openid_connect_provider.github.arn]) &&
      length(one(data.aws_iam_policy_document.edge_assume.statement).condition) == 2 &&
      alltrue([
        for condition in one(data.aws_iam_policy_document.edge_assume.statement).condition :
        condition.test == "StringEquals" && (
          condition.variable == "token.actions.githubusercontent.com:aud"
          ? (
            length(condition.values) == 1 &&
            one(condition.values) == "sts.amazonaws.com"
          )
          : (
            condition.variable == "token.actions.githubusercontent.com:sub" &&
            length(condition.values) == 1 &&
            one(condition.values) == "repo:ohchanwu/jobcron:environment:edge"
          )
        )
      ])
    )
    error_message = "Edge trust must require the GitHub audience and edge environment."
  }

  assert {
    condition = (
      length(data.aws_iam_policy_document.production_state.statement) == 4 &&
      data.aws_iam_policy_document.production_state.statement[0].actions ==
      toset(["s3:GetBucketLocation"]) &&
      data.aws_iam_policy_document.production_state.statement[1].actions ==
      toset(["s3:ListBucket"]) &&
      data.aws_iam_policy_document.production_state.statement[2].actions ==
      toset(["s3:GetObject", "s3:PutObject"]) &&
      data.aws_iam_policy_document.production_state.statement[3].actions ==
      toset(["s3:DeleteObject"])
    )
    error_message = "Production state policy must contain only the four approved action groups."
  }

  assert {
    condition = (
      length(data.aws_iam_policy_document.production_network_read.statement) == 1 &&
      one(
        jsondecode(data.aws_iam_policy_document.production_network_read.json).Statement,
      ).Effect ==
      "Allow" &&
      coalesce(
        one(data.aws_iam_policy_document.production_network_read.statement).effect,
        "Allow",
      ) ==
      "Allow" &&
      one(data.aws_iam_policy_document.production_network_read.statement).resources ==
      toset(["*"]) &&
      one(data.aws_iam_policy_document.production_network_read.statement).actions ==
      toset([
        "ec2:DescribeAddresses",
        "ec2:DescribeAvailabilityZones",
        "ec2:DescribeInternetGateways",
        "ec2:DescribeRouteTables",
        "ec2:DescribeSubnetAttribute",
        "ec2:DescribeSubnets",
        "ec2:DescribeTags",
        "ec2:DescribeVpcAttribute",
        "ec2:DescribeVpcs",
      ])
    )
    error_message = "Production network reads must remain within the approved EC2 Describe ceiling."
  }

  assert {
    condition = (
      aws_iam_policy.production_network_read.policy ==
      data.aws_iam_policy_document.production_network_read.json &&
      aws_iam_role_policy_attachment.production_network_read.role ==
      aws_iam_role.production.name &&
      aws_iam_role_policy_attachment.production_network_read.policy_arn ==
      aws_iam_policy.production_network_read.arn
    )
    error_message = "Production must attach only the approved network-read policy document."
  }

  assert {
    condition = (
      length(data.aws_iam_policy_document.edge_state.statement) == 4 &&
      data.aws_iam_policy_document.edge_state.statement[0].actions ==
      toset(["s3:GetBucketLocation"]) &&
      data.aws_iam_policy_document.edge_state.statement[1].actions ==
      toset(["s3:ListBucket"]) &&
      data.aws_iam_policy_document.edge_state.statement[2].actions ==
      toset(["s3:GetObject", "s3:PutObject"]) &&
      data.aws_iam_policy_document.edge_state.statement[3].actions ==
      toset(["s3:DeleteObject"])
    )
    error_message = "Edge state policy must contain only the four approved action groups."
  }

  assert {
    condition = (
      length(data.aws_iam_policy_document.production_state.statement[0].condition) == 0 &&
      length(data.aws_iam_policy_document.production_state.statement[1].condition) == 1 &&
      one(data.aws_iam_policy_document.production_state.statement[1].condition).test ==
      "StringLike" &&
      one(data.aws_iam_policy_document.production_state.statement[1].condition).variable ==
      "s3:prefix" &&
      toset(one(data.aws_iam_policy_document.production_state.statement[1].condition).values) ==
      toset([
        "production/terraform.tfstate",
        "production/terraform.tfstate.tflock",
      ])
    )
    error_message = "Production must scope ListBucket by prefix without conditioning GetBucketLocation."
  }

  assert {
    condition = (
      length(data.aws_iam_policy_document.edge_state.statement[0].condition) == 0 &&
      length(data.aws_iam_policy_document.edge_state.statement[1].condition) == 1 &&
      one(data.aws_iam_policy_document.edge_state.statement[1].condition).test ==
      "StringLike" &&
      one(data.aws_iam_policy_document.edge_state.statement[1].condition).variable ==
      "s3:prefix" &&
      toset(one(data.aws_iam_policy_document.edge_state.statement[1].condition).values) ==
      toset(concat(local.edge_state_keys, local.edge_lock_keys))
    )
    error_message = "Edge must scope ListBucket by prefix without conditioning GetBucketLocation."
  }

  assert {
    condition = (
      data.aws_iam_policy_document.production_state.statement[0].resources ==
      toset([aws_s3_bucket.state.arn]) &&
      data.aws_iam_policy_document.production_state.statement[1].resources ==
      toset([aws_s3_bucket.state.arn]) &&
      data.aws_iam_policy_document.production_state.statement[2].resources ==
      toset([
        "${aws_s3_bucket.state.arn}/production/terraform.tfstate",
        "${aws_s3_bucket.state.arn}/production/terraform.tfstate.tflock",
      ]) &&
      data.aws_iam_policy_document.production_state.statement[3].resources ==
      toset([
        "${aws_s3_bucket.state.arn}/production/terraform.tfstate.tflock",
      ])
    )
    error_message = "Production may delete only its lock files and access only approved state keys."
  }

  assert {
    condition = (
      data.aws_iam_policy_document.edge_state.statement[0].resources ==
      toset([aws_s3_bucket.state.arn]) &&
      data.aws_iam_policy_document.edge_state.statement[1].resources ==
      toset([aws_s3_bucket.state.arn]) &&
      data.aws_iam_policy_document.edge_state.statement[2].resources ==
      toset([
        "${aws_s3_bucket.state.arn}/edge/terraform.tfstate",
        "${aws_s3_bucket.state.arn}/edge/terraform.tfstate.tflock",
      ]) &&
      data.aws_iam_policy_document.edge_state.statement[3].resources ==
      toset([
        "${aws_s3_bucket.state.arn}/edge/terraform.tfstate.tflock",
      ])
    )
    error_message = "Edge may delete only its lock file and access only edge state keys."
  }

  assert {
    condition = (
      aws_iam_role_policy_attachment.production_state.role ==
      aws_iam_role.production.name &&
      aws_iam_role_policy_attachment.production_state.policy_arn ==
      aws_iam_policy.production_state.arn &&
      aws_iam_role_policy_attachment.edge_state.role ==
      aws_iam_role.edge.name &&
      aws_iam_role_policy_attachment.edge_state.policy_arn ==
      aws_iam_policy.edge_state.arn
    )
    error_message = "Each environment role must attach only its matching state policy."
  }
}

run "existing_provider_configuration" {
  command = plan

  variables {
    existing_github_oidc_provider_arn = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
  }

  assert {
    condition = (
      aws_iam_openid_connect_provider.github.url ==
      "https://token.actions.githubusercontent.com" &&
      aws_iam_openid_connect_provider.github.client_id_list ==
      toset(["sts.amazonaws.com"])
    )
    error_message = "Existing-provider adoption must retain GitHub's URL and STS audience."
  }
}
