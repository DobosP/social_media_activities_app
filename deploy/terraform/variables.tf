# --- provider / box ---
variable "hcloud_token" {
  description = "Hetzner Cloud API token (Project > Security > API Tokens, read+write)."
  type        = string
  sensitive   = true
}

variable "ssh_public_key" {
  description = "SSH public key authorized for the box (key-only auth; passwords are disabled)."
  type        = string
}

variable "admin_ip" {
  description = "CIDR allowed to reach SSH (port 22), e.g. \"203.0.113.4/32\". 80/443 are open to all."
  type        = string
}

variable "location" {
  description = "Hetzner EU location: fsn1 / nbg1 (Germany) or hel1 (Finland). EU data residency."
  type        = string
  default     = "fsn1"
}

variable "server_type" {
  description = "Hetzner server type — must be REAL for your project (run `hcloud server-type list`). cpx21 (3 vCPU/4 GB AMD, ~EUR 8/mo); the cheaper cx22 (2 vCPU/4 GB Intel) also fits."
  type        = string
  default     = "cpx21"
}

variable "domain" {
  description = "Public hostname (an A/AAAA record you point at the box's IP). Caddy provisions TLS."
  type        = string
}

variable "app_repo_url" {
  description = "Git clone URL for this repo. For a PRIVATE repo embed a read-only deploy token."
  type        = string
}

# --- app secrets (rendered into the box's .env; Terraform STATE will contain these — store it
#     securely, e.g. an encrypted remote backend) ---
variable "db_password" {
  description = "Password for the Postgres 'app' role (local-only DB). It is interpolated into the DATABASE_URL DSN AND the CREATE ROLE literal, so it must be URL- and SQL-safe — use `openssl rand -hex 32`."
  type        = string
  sensitive   = true
  validation {
    # URL-unreserved + SQL/shell-safe: no quotes, @, :, /, #, ?, $, backtick, spaces, etc.
    condition     = can(regex("^[A-Za-z0-9._~-]{12,}$", var.db_password))
    error_message = "db_password must be >=12 chars from [A-Za-z0-9._~-] (URL/SQL-safe). Generate: openssl rand -hex 32."
  }
}

variable "django_secret_key" {
  description = "DJANGO_SECRET_KEY (e.g. `openssl rand -hex 48`). prod.py fails to boot on the default."
  type        = string
  sensitive   = true
}

# --- EU object storage (Hetzner Object Storage / R2 / MinIO) ---
variable "media_s3_bucket" {
  description = "Private bucket for media blobs."
  type        = string
}

variable "media_s3_endpoint_url" {
  description = "S3 endpoint, e.g. https://fsn1.your-objectstorage.com (Hetzner)."
  type        = string
}

variable "media_s3_region" {
  description = "Region; prod.py requires it to start 'eu' OR a non-empty endpoint (minors' residency)."
  type        = string
  default     = "eu-central"
}

variable "media_s3_sse" {
  description = "Server-side encryption at rest (e.g. \"AES256\") where the provider supports it; \"\" to skip."
  type        = string
  default     = ""
}

variable "aws_access_key_id" {
  description = "S3 access key for the bucket (boto3 default credential chain)."
  type        = string
  sensitive   = true
}

variable "aws_secret_access_key" {
  description = "S3 secret key."
  type        = string
  sensitive   = true
}

# --- identity / observability / retention ---
variable "eudi_trusted_issuers" {
  description = "JSON map of EUDI issuer URL -> public key. prod.py HARD-fails if empty with the EUDI provider."
  type        = string
  sensitive   = true
}

variable "sentry_dsn" {
  description = "Sentry DSN for error tracking (\"\" disables)."
  type        = string
  default     = ""
  sensitive   = true
}

variable "metrics_token" {
  description = "Bearer token gating /metrics (\"\" => /metrics returns 403, the safe default)."
  type        = string
  default     = ""
  sensitive   = true
}

variable "messaging_retention_days" {
  description = "E2EE messaging retention period in days (your DPO sets it; 0 disables that purge)."
  type        = number
  default     = 365
}

variable "ops_heartbeat_url" {
  description = "Dead-man's-switch URL pinged after a fully-successful run_due_jobs (e.g. a healthchecks.io ping). \"\" disables."
  type        = string
  default     = ""
}
