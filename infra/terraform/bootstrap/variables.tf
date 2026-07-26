variable "state_bucket_name" {
  description = "Globally unique private S3 bucket used only for Terraform state."
  type        = string

  validation {
    condition = (
      length(var.state_bucket_name) >= 3 &&
      length(var.state_bucket_name) <= 63 &&
      can(regex("^[a-z0-9][a-z0-9.-]*[a-z0-9]$", var.state_bucket_name)) &&
      !strcontains(var.state_bucket_name, "..") &&
      !can(regex("^[0-9]{1,3}(\\.[0-9]{1,3}){3}$", var.state_bucket_name)) &&
      !startswith(var.state_bucket_name, "xn--") &&
      !startswith(var.state_bucket_name, "sthree-") &&
      !startswith(var.state_bucket_name, "amzn_s3_demo_") &&
      !endswith(var.state_bucket_name, "-s3alias") &&
      !endswith(var.state_bucket_name, "--ol-s3") &&
      !endswith(var.state_bucket_name, ".mrap") &&
      !endswith(var.state_bucket_name, "--x-s3") &&
      !endswith(var.state_bucket_name, "--table-s3")
    )
    error_message = "state_bucket_name must satisfy the S3 general-purpose bucket naming rules."
  }
}

variable "github_repository" {
  description = "GitHub repository in owner/name form."
  type        = string
  default     = "ohchanwu/jobcron"

  validation {
    condition = can(regex(
      "^[A-Za-z0-9]([A-Za-z0-9-]{0,37}[A-Za-z0-9])?/[A-Za-z0-9._-]{1,100}$",
      var.github_repository,
    ))
    error_message = "github_repository must use a valid GitHub owner/name slug."
  }
}

variable "existing_github_oidc_provider_arn" {
  description = "Existing GitHub OIDC provider ARN to adopt, or null to create it."
  type        = string
  default     = null

  validation {
    condition = (
      var.existing_github_oidc_provider_arn == null ||
      endswith(
        var.existing_github_oidc_provider_arn,
        ":oidc-provider/token.actions.githubusercontent.com",
      )
    )
    error_message = "The existing provider must be GitHub's OIDC provider."
  }
}
