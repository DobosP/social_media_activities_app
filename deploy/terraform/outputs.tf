output "server_ipv4" {
  description = "Create an A record for var.domain pointing here, then Caddy auto-provisions TLS."
  value       = hcloud_server.app.ipv4_address
}

output "server_ipv6" {
  description = "Optional AAAA record target."
  value       = hcloud_server.app.ipv6_address
}

output "next_steps" {
  value = "1) point ${var.domain} (A/AAAA) at the IP above; 2) cloud-init installs Postgres+PostGIS+pgvector+Redis+daphne+Caddy and starts the app; 3) watch with: ssh root@<ip> 'cloud-init status --wait && journalctl -u socialapp -f'."
}
