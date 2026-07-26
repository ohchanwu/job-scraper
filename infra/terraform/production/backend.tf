terraform {
  backend "s3" {
    key          = "production/terraform.tfstate"
    use_lockfile = true
    encrypt      = true
  }
}
