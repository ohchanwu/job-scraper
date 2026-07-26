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
    json = "production-assume"
  }
}

override_data {
  target          = data.aws_iam_policy_document.edge_assume
  override_during = plan
  values = {
    json = "edge-assume"
  }
}

override_data {
  target          = data.aws_iam_policy_document.production_state
  override_during = plan
  values = {
    json = "production-state"
  }
}

override_data {
  target          = data.aws_iam_policy_document.edge_state
  override_during = plan
  values = {
    json = "edge-state"
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
      aws_iam_role.production.assume_role_policy ==
      data.aws_iam_policy_document.production_assume.json
    )
    error_message = "Production must use only its production trust document."
  }

  assert {
    condition = (
      aws_iam_role.edge.assume_role_policy ==
      data.aws_iam_policy_document.edge_assume.json
    )
    error_message = "Edge must use only its edge trust document."
  }

  assert {
    condition = (
      length(data.aws_iam_policy_document.production_assume.statement) == 1 &&
      one(data.aws_iam_policy_document.production_assume.statement).actions ==
      toset(["sts:AssumeRoleWithWebIdentity"]) &&
      length(one(data.aws_iam_policy_document.production_assume.statement).principals) == 1 &&
      one(one(data.aws_iam_policy_document.production_assume.statement).principals).type ==
      "Federated" &&
      length(one(data.aws_iam_policy_document.production_assume.statement).condition) == 2 &&
      alltrue([
        for condition in one(data.aws_iam_policy_document.production_assume.statement).condition :
        condition.test == "StringEquals" && (
          condition.variable == "token.actions.githubusercontent.com:aud"
          ? condition.values == ["sts.amazonaws.com"]
          : (
            condition.variable == "token.actions.githubusercontent.com:sub" &&
            condition.values == ["repo:ohchanwu/jobcron:environment:production"]
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
      length(one(data.aws_iam_policy_document.edge_assume.statement).condition) == 2 &&
      alltrue([
        for condition in one(data.aws_iam_policy_document.edge_assume.statement).condition :
        condition.test == "StringEquals" && (
          condition.variable == "token.actions.githubusercontent.com:aud"
          ? condition.values == ["sts.amazonaws.com"]
          : (
            condition.variable == "token.actions.githubusercontent.com:sub" &&
            condition.values == ["repo:ohchanwu/jobcron:environment:edge"]
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
      data.aws_iam_policy_document.production_state.statement[0].resources ==
      toset([aws_s3_bucket.state.arn]) &&
      data.aws_iam_policy_document.production_state.statement[1].resources ==
      toset([aws_s3_bucket.state.arn]) &&
      data.aws_iam_policy_document.production_state.statement[2].resources ==
      toset([
        "${aws_s3_bucket.state.arn}/bootstrap/terraform.tfstate",
        "${aws_s3_bucket.state.arn}/bootstrap/terraform.tfstate.tflock",
        "${aws_s3_bucket.state.arn}/production/terraform.tfstate",
        "${aws_s3_bucket.state.arn}/production/terraform.tfstate.tflock",
      ]) &&
      data.aws_iam_policy_document.production_state.statement[3].resources ==
      toset([
        "${aws_s3_bucket.state.arn}/bootstrap/terraform.tfstate.tflock",
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
