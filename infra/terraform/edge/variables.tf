variable "cloudflare_ipv4_cidrs" {
  description = "Validated public Cloudflare IPv4 CIDRs."
  type        = list(string)

  validation {
    condition = (
      length(var.cloudflare_ipv4_cidrs) >= 10 &&
      length(var.cloudflare_ipv4_cidrs) <= 20 &&
      length(distinct(var.cloudflare_ipv4_cidrs)) ==
      length(var.cloudflare_ipv4_cidrs)
    )
    error_message = "Cloudflare IPv4 CIDRs must contain 10 through 20 unique entries."
  }
}
