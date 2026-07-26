mock_provider "aws" {}

variables {
  state_bucket_name = "jobcron-state-test-only"
}

override_resource {
  target          = aws_s3_bucket.state
  override_during = plan
  values = {
    arn = "arn:aws:s3:::jobcron-state-test-only"
  }
}

run "state_contract" {
  command = plan

  assert {
    condition = (
      aws_s3_bucket_public_access_block.state.block_public_acls &&
      aws_s3_bucket_public_access_block.state.block_public_policy &&
      aws_s3_bucket_public_access_block.state.ignore_public_acls &&
      aws_s3_bucket_public_access_block.state.restrict_public_buckets
    )
    error_message = "State bucket must enable all four public-access blocks."
  }

  assert {
    condition     = aws_s3_bucket_versioning.state.versioning_configuration[0].status == "Enabled"
    error_message = "State bucket versioning must be enabled."
  }

  assert {
    condition     = one(aws_s3_bucket_server_side_encryption_configuration.state.rule).apply_server_side_encryption_by_default[0].sse_algorithm == "AES256"
    error_message = "State bucket must enable default encryption."
  }

  assert {
    condition = (
      one(data.aws_iam_policy_document.state_bucket.statement).sid == "DenyInsecureTransport" &&
      one(data.aws_iam_policy_document.state_bucket.statement).effect == "Deny" &&
      toset(one(data.aws_iam_policy_document.state_bucket.statement).actions) == toset(["s3:*"]) &&
      length(one(data.aws_iam_policy_document.state_bucket.statement).resources) == 2 &&
      one(one(data.aws_iam_policy_document.state_bucket.statement).principals).type == "*" &&
      toset(one(one(data.aws_iam_policy_document.state_bucket.statement).principals).identifiers) == toset(["*"]) &&
      one(one(data.aws_iam_policy_document.state_bucket.statement).condition).test == "Bool" &&
      one(one(data.aws_iam_policy_document.state_bucket.statement).condition).variable == "aws:SecureTransport" &&
      toset(one(one(data.aws_iam_policy_document.state_bucket.statement).condition).values) == toset(["false"])
    )
    error_message = "State bucket policy must deny insecure transport for every principal and S3 action."
  }
}
