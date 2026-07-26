mock_provider "aws" {}

variables {
  state_bucket_name = "jobcron-state-test-only"
}

run "state_contract" {
  command = plan

  assert {
    condition     = aws_s3_bucket_public_access_block.state.block_public_policy
    error_message = "State bucket must block public bucket policies."
  }

  assert {
    condition     = aws_s3_bucket_versioning.state.versioning_configuration[0].status == "Enabled"
    error_message = "State bucket versioning must be enabled."
  }

  assert {
    condition     = one(aws_s3_bucket_server_side_encryption_configuration.state.rule).apply_server_side_encryption_by_default[0].sse_algorithm == "AES256"
    error_message = "State bucket must enable default encryption."
  }
}
