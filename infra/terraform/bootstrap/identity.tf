import {
  for_each = (
    var.existing_github_oidc_provider_arn == null
    ? {}
    : { github = var.existing_github_oidc_provider_arn }
  )

  to = aws_iam_openid_connect_provider.github
  id = each.value
}

resource "aws_iam_openid_connect_provider" "github" {
  url = "https://token.actions.githubusercontent.com"

  client_id_list = ["sts.amazonaws.com"]

  lifecycle {
    prevent_destroy = true
  }
}

data "aws_iam_policy_document" "production_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repository}:environment:production"]
    }
  }
}

resource "aws_iam_role" "production" {
  name               = "JobcronTerraformProduction"
  assume_role_policy = data.aws_iam_policy_document.production_assume.json
}

data "aws_iam_policy_document" "edge_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repository}:environment:edge"]
    }
  }
}

resource "aws_iam_role" "edge" {
  name               = "JobcronTerraformEdge"
  assume_role_policy = data.aws_iam_policy_document.edge_assume.json
}

locals {
  production_state_keys = [
    "production/terraform.tfstate",
  ]
  production_lock_keys = [
    "production/terraform.tfstate.tflock",
  ]

  edge_state_keys = [
    "edge/terraform.tfstate",
  ]
  edge_lock_keys = [
    "edge/terraform.tfstate.tflock",
  ]
}

data "aws_iam_policy_document" "production_state" {
  statement {
    actions   = ["s3:GetBucketLocation"]
    resources = [aws_s3_bucket.state.arn]
  }

  statement {
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.state.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values = concat(
        local.production_state_keys,
        local.production_lock_keys,
      )
    }
  }

  statement {
    actions = [
      "s3:GetObject",
      "s3:PutObject",
    ]
    resources = [
      for key in concat(
        local.production_state_keys,
        local.production_lock_keys,
      ) :
      "${aws_s3_bucket.state.arn}/${key}"
    ]
  }

  statement {
    actions = ["s3:DeleteObject"]
    resources = [
      for key in local.production_lock_keys :
      "${aws_s3_bucket.state.arn}/${key}"
    ]
  }
}

resource "aws_iam_policy" "production_state" {
  name   = "JobcronTerraformProductionState"
  policy = data.aws_iam_policy_document.production_state.json
}

resource "aws_iam_role_policy_attachment" "production_state" {
  role       = aws_iam_role.production.name
  policy_arn = aws_iam_policy.production_state.arn
}

data "aws_iam_policy_document" "edge_state" {
  statement {
    actions   = ["s3:GetBucketLocation"]
    resources = [aws_s3_bucket.state.arn]
  }

  statement {
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.state.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values = concat(
        local.edge_state_keys,
        local.edge_lock_keys,
      )
    }
  }

  statement {
    actions = [
      "s3:GetObject",
      "s3:PutObject",
    ]
    resources = [
      for key in concat(
        local.edge_state_keys,
        local.edge_lock_keys,
      ) :
      "${aws_s3_bucket.state.arn}/${key}"
    ]
  }

  statement {
    actions = ["s3:DeleteObject"]
    resources = [
      for key in local.edge_lock_keys :
      "${aws_s3_bucket.state.arn}/${key}"
    ]
  }
}

resource "aws_iam_policy" "edge_state" {
  name   = "JobcronTerraformEdgeState"
  policy = data.aws_iam_policy_document.edge_state.json
}

resource "aws_iam_role_policy_attachment" "edge_state" {
  role       = aws_iam_role.edge.name
  policy_arn = aws_iam_policy.edge_state.arn
}

data "aws_iam_policy_document" "production_network_read" {
  statement {
    actions = [
      "ec2:DescribeAddresses",
      "ec2:DescribeAvailabilityZones",
      "ec2:DescribeInternetGateways",
      "ec2:DescribeRouteTables",
      "ec2:DescribeSubnetAttribute",
      "ec2:DescribeSubnets",
      "ec2:DescribeTags",
      "ec2:DescribeVpcAttribute",
      "ec2:DescribeVpcs",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_policy" "production_network_read" {
  name   = "JobcronTerraformProductionNetworkRead"
  policy = data.aws_iam_policy_document.production_network_read.json
}

resource "aws_iam_role_policy_attachment" "production_network_read" {
  role       = aws_iam_role.production.name
  policy_arn = aws_iam_policy.production_network_read.arn
}
