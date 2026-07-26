mock_provider "aws" {}

run "reject_invalid_state_bucket_name" {
  command = plan

  variables {
    state_bucket_name = "INVALID_"
  }

  expect_failures = [var.state_bucket_name]
}

run "reject_invalid_github_repository" {
  command = plan

  variables {
    state_bucket_name = "jobcron-state-test-only"
    github_repository = "bad repo/name"
  }

  expect_failures = [var.github_repository]
}
