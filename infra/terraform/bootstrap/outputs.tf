output "state_bucket_name" {
  value     = aws_s3_bucket.state.id
  sensitive = true
}

output "production_role_arn" {
  value     = aws_iam_role.production.arn
  sensitive = true
}

output "edge_role_arn" {
  value     = aws_iam_role.edge.arn
  sensitive = true
}
