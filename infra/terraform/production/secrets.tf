resource "aws_secretsmanager_secret" "runtime" {
  name                    = var.private_database_config.runtime_secret_name
  recovery_window_in_days = 30

  lifecycle {
    prevent_destroy = true
  }
}
