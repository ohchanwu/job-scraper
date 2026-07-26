variable "state_bucket_name" {
  description = "Globally unique private S3 bucket used only for Terraform state."
  type        = string

  validation {
    condition     = length(var.state_bucket_name) >= 3
    error_message = "state_bucket_name must be a valid non-empty S3 bucket name."
  }
}

variable "github_repository" {
  description = "GitHub repository in owner/name form."
  type        = string
  default     = "ohchanwu/jobcron"

  validation {
    condition     = can(regex("^[^/]+/[^/]+$", var.github_repository))
    error_message = "github_repository must use owner/name form."
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
