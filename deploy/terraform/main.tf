resource "hcloud_ssh_key" "admin" {
  name       = "socialapp-admin"
  public_key = var.ssh_public_key
}

# Cloud firewall: SSH only from the admin CIDR; HTTP/HTTPS open (Caddy + Let's Encrypt). Postgres
# and Redis are NOT opened — they stay bound to localhost on the box.
resource "hcloud_firewall" "socialapp" {
  name = "socialapp"

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "22"
    source_ips = [var.admin_ip]
  }
  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "80"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "443"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
}

resource "hcloud_server" "app" {
  name         = "socialapp"
  image        = "ubuntu-24.04"
  server_type  = var.server_type
  location     = var.location
  ssh_keys     = [hcloud_ssh_key.admin.id]
  firewall_ids = [hcloud_firewall.socialapp.id]
  labels       = { app = "socialapp" }

  user_data = templatefile("${path.module}/../cloud-init.yaml.tftpl", {
    domain                   = var.domain
    app_repo_url             = var.app_repo_url
    db_password              = var.db_password
    django_secret_key        = var.django_secret_key
    media_s3_bucket          = var.media_s3_bucket
    media_s3_endpoint_url    = var.media_s3_endpoint_url
    media_s3_region          = var.media_s3_region
    media_s3_sse             = var.media_s3_sse
    aws_access_key_id        = var.aws_access_key_id
    aws_secret_access_key    = var.aws_secret_access_key
    eudi_trusted_issuers     = var.eudi_trusted_issuers
    sentry_dsn               = var.sentry_dsn
    metrics_token            = var.metrics_token
    messaging_retention_days = var.messaging_retention_days
    ops_heartbeat_url        = var.ops_heartbeat_url
  })
}
