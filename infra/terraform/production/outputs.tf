output "replacement_instance_id" {
  description = "Sensitive selector for Session Manager operations."
  value       = aws_instance.replacement_host.id
  sensitive   = true
}
