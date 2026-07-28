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
      "ec2:DescribeAddressesAttribute",
      "ec2:DescribeAvailabilityZones",
      "ec2:DescribeInternetGateways",
      "ec2:DescribeNetworkAcls",
      "ec2:DescribeRouteTables",
      "ec2:DescribeSecurityGroups",
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

data "aws_iam_policy_document" "production_slice3_read" {
  statement {
    actions = [
      "ec2:DescribeSecurityGroupRules",
      "rds:DescribeDBEngineVersions",
      "rds:DescribeDBInstances",
      "rds:DescribeDBParameterGroups",
      "rds:DescribeDBParameters",
      "rds:DescribeDBSubnetGroups",
      "rds:DescribeOrderableDBInstanceOptions",
      "rds:ListTagsForResource",
      "s3:GetAccelerateConfiguration",
      "s3:GetBucketAcl",
      "s3:GetBucketCORS",
      "s3:GetBucketLocation",
      "s3:GetBucketLogging",
      "s3:GetBucketObjectLockConfiguration",
      "s3:GetBucketOwnershipControls",
      "s3:GetBucketPolicy",
      "s3:GetBucketPolicyStatus",
      "s3:GetBucketPublicAccessBlock",
      "s3:GetBucketRequestPayment",
      "s3:GetBucketTagging",
      "s3:GetBucketVersioning",
      "s3:GetBucketWebsite",
      "s3:GetEncryptionConfiguration",
      "s3:GetLifecycleConfiguration",
      "s3:GetReplicationConfiguration",
      "s3:ListBucket",
      "secretsmanager:DescribeSecret",
      "secretsmanager:GetResourcePolicy",
      "secretsmanager:ListSecretVersionIds",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_policy" "production_slice3_read" {
  name   = "JobcronTerraformProductionSlice3Read"
  policy = data.aws_iam_policy_document.production_slice3_read.json

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_iam_role_policy_attachment" "production_slice3_read" {
  role       = aws_iam_role.production.name
  policy_arn = aws_iam_policy.production_slice3_read.arn
}

data "aws_caller_identity" "current" {}

locals {
  edge_prefix_list_arn = (
    "arn:aws:ec2:ap-northeast-2:${data.aws_caller_identity.current.account_id}:prefix-list/*"
  )
  edge_security_group_arn = (
    "arn:aws:ec2:ap-northeast-2:${data.aws_caller_identity.current.account_id}:security-group/*"
  )
  edge_security_group_rule_arn = (
    "arn:aws:ec2:ap-northeast-2:${data.aws_caller_identity.current.account_id}:security-group-rule/*"
  )
}

data "aws_iam_policy_document" "edge_prefix_list" {
  statement {
    actions = [
      "ec2:DescribeManagedPrefixLists",
      "ec2:DescribeSecurityGroups",
      "ec2:DescribeSecurityGroupRules",
      "ec2:DescribeTags",
    ]
    resources = ["*"]
  }

  statement {
    actions   = ["ec2:GetManagedPrefixListEntries"]
    resources = [local.edge_prefix_list_arn]

    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/jobcron:edge-source"
      values   = ["cloudflare-ipv4"]
    }
  }

  statement {
    actions   = ["ec2:CreateManagedPrefixList"]
    resources = [local.edge_prefix_list_arn]

    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/jobcron:edge-source"
      values   = ["cloudflare-ipv4"]
    }

    condition {
      test     = "ForAllValues:StringEquals"
      variable = "aws:TagKeys"
      values   = ["jobcron:edge-source"]
    }
  }

  statement {
    actions   = ["ec2:ModifyManagedPrefixList"]
    resources = [local.edge_prefix_list_arn]

    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/jobcron:edge-source"
      values   = ["cloudflare-ipv4"]
    }
  }

  statement {
    actions   = ["ec2:AuthorizeSecurityGroupIngress"]
    resources = [local.edge_security_group_arn]

    condition {
      test     = "StringEquals"
      variable = "aws:ResourceTag/jobcron:edge-target"
      values   = ["origin-security-group"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/jobcron:edge-rule"
      values   = ["origin-https-from-cloudflare"]
    }

    condition {
      test     = "ForAllValues:StringEquals"
      variable = "aws:TagKeys"
      values   = ["jobcron:edge-rule"]
    }
  }

  statement {
    actions   = ["ec2:CreateTags"]
    resources = [local.edge_prefix_list_arn]

    condition {
      test     = "StringEquals"
      variable = "ec2:CreateAction"
      values   = ["CreateManagedPrefixList"]
    }

    condition {
      test     = "ForAllValues:StringEquals"
      variable = "aws:TagKeys"
      values   = ["jobcron:edge-source"]
    }
  }

  statement {
    actions   = ["ec2:CreateTags"]
    resources = [local.edge_security_group_rule_arn]

    condition {
      test     = "StringEquals"
      variable = "ec2:CreateAction"
      values   = ["AuthorizeSecurityGroupIngress"]
    }

    condition {
      test     = "ForAllValues:StringEquals"
      variable = "aws:TagKeys"
      values   = ["jobcron:edge-rule"]
    }
  }
}

resource "aws_iam_policy" "edge_prefix_list" {
  name   = "JobcronTerraformEdgePrefixList"
  policy = data.aws_iam_policy_document.edge_prefix_list.json

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_iam_role_policy_attachment" "edge_prefix_list" {
  role       = aws_iam_role.edge.name
  policy_arn = aws_iam_policy.edge_prefix_list.arn
}
