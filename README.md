# registry-server

CognitiveOS `.cgp` package registry — a Go HTTP server for hosting, searching, versioning, and distributing cognitive patches with license/code unlock support.

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/health` | Healthcheck |
| GET | `/v1/search?q=` | Search patches |
| GET | `/v1/patches/:name` | Get patch metadata |
| GET | `/v1/patches/:name/versions` | List all versions |
| GET | `/v1/patches/:name/:version` | Get specific version |
| GET | `/v1/patches/:name/:version/download` | Download .cgp archive |
| GET | `/v1/patches/:name/dependencies` | Get dependency graph |
| GET | `/v1/notary/check` | Check notary checksum |
| POST | `/v1/auth/register` | Register SSH public key |
| POST | `/v1/patches` | Publish new patch |
| PUT | `/v1/patches/:name/:version` | Publish new version |
| PATCH | `/v1/patches/:name/:version/status` | Set version status (admin) |
| POST | `/v1/patches/:name/:version/validate` | Validate checksum (admin) |
| POST | `/v1/patches/:name/:version/unlock` | Unlock paid/supporter patch |

## Authentication

- **Public:** Read access for search, metadata, and download
- **Token-based:** Publishing requires a valid token (legacy, still active)
- **SSH key-based:** Publishers register SSH public keys via `/v1/auth/register`; signatures verified via SSHSIG protocol
- **Code unlock:** Paid/supporter-only patches use unlock codes

## Rate Limiting

All endpoints are rate-limited per IP. Limits are intentionally restrictive:

| Endpoint | Limit |
|----------|-------|
| Read (search, metadata) | 10 req/min |
| Download | 5 req/min |
| Notary check | 5 req/min |
| Publish | 2 req/min |
| Unlock | 2 req/min |
| Auth register | 1 req/min |
| Healthcheck | exempt |
| **Global** | **30 req/min** |

Rate limit headers are included in every response:
- `X-RateLimit-Limit`: maximum requests per window
- `X-RateLimit-Remaining`: requests remaining
- `X-RateLimit-Reset`: seconds until window resets

See [Fair Use Policy](https://github.com/CognitiveOS-Project/product-specs/blob/main/specs/fair-use-policy.md).

## Anti-Bot Protection

The server applies layered defense:

1. **User-Agent filtering** — blocks empty or known-malicious User-Agents
2. **Path probing protection** — blocks `.env`, `.git`, `wp-admin`, and similar paths
3. **Request size limits** — 32 MB max body size

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Listen port |
| `DATA_DIR` | `./data` | Data directory for patches and metadata |
| `S3_ENDPOINT` | — | S3-compatible endpoint (Cloudflare R2, MinIO, etc.) |
| `S3_BUCKET` | `cognitiveos-registry` | S3 bucket name |
| `S3_ACCESS_KEY` | — | S3 access key ID |
| `S3_SECRET_KEY` | — | S3 secret access key |
| `S3_REGION` | `auto` | S3 region |
| `BASE_DOMAIN` | `cognitive-os.org` | Base domain for URLs |
| `REGISTRY_GH_TOKEN` | — | GitHub PAT for creating releases |
| `REGISTRY_GH_ORG` | — | GitHub org for package releases |

Command-line flags override env vars:

```bash
./registry-server -addr :9090 -data-dir /var/data
```

## Build

```bash
make build    # Compile to build/bin/registry-server
make test     # Run tests
make lint     # Run go vet
make clean    # Remove build artifacts
```

## Docker

### Application Image

```bash
docker build -t registry-server .
docker run -p 8080:8080 registry-server
```

The Dockerfile uses a multi-stage build:
- **Build stage:** `golang:1.25` with `CGO_ENABLED=0` for a static binary
- **Runtime stage:** `gcr.io/distroless/static-debian12` (~10 MB image)

### Setup Image (GCP)

For running Google Cloud setup scripts in a containerized environment:

```bash
docker build -f Dockerfile-gcloud -t registry-gcloud-setup .
docker run -it registry-gcloud-setup scripts/google-cloud/setup-project.sh
```

This image includes:
- `google/cloud-sdk` (gcloud, gsutil)
- `scripts/google-cloud/` (GCP project + service account setup)

### Setup Image (Cloudflare)

For Cloudflare R2 setup without local rclone installation:

```bash
docker build -f Dockerfile-cloudflare -t registry-cloudflare-setup .
docker run -it registry-cloudflare-setup scripts/cloudflare/setup-r2.sh
```

This image includes:
- `rclone` (S3-compatible storage tool)
- `scripts/cloudflare/` (R2 bucket + API token setup)

## Deployment

### Google Cloud Run (Primary)

Deployment is automated via GitHub Actions. Push to `main` triggers `deploy-cloud-run.yml`.

#### Step 1: Google Cloud Setup

```bash
# Requires: gcloud CLI installed and authenticated
# See: https://cloud.google.com/sdk/docs/install
./scripts/google-cloud/setup-project.sh
```

The script will:
1. Prompt for your GCP project ID (or use current)
2. Enable Cloud Run, Container Registry, and Cloud Build APIs
3. Create a `registry-deployer` service account
4. Grant `roles/run.admin` and `roles/storage.admin`
5. Generate a JSON key at `/tmp/registry-deployer-key.json`
6. Print the secrets to add to GitHub

#### Step 2: Cloudflare R2 Setup

```bash
# Interactive — guides you through Cloudflare dashboard steps
./scripts/cloudflare/setup-r2.sh
```

The script will:
1. Guide you to create an R2 bucket (prompted for name, default: `cognitiveos-registry`)
2. Configure public access for notary metadata
3. Create an API token with Object Read & Write
4. Ask for your R2 Account ID
5. Print the secrets to add to GitHub

#### Step 3: Add GitHub Secrets

Go to [github.com/CognitiveOS-Project/registry-server](https://github.com/CognitiveOS-Project/registry-server) → Settings → Secrets and variables → Actions → New repository secret.

| Secret | Source | Description |
|--------|--------|-------------|
| `GCP_PROJECT_ID` | GCP Console | Google Cloud project ID |
| `GCP_SA_KEY` | `cat /tmp/registry-deployer-key.json` | Service account JSON key |
| `BASE_DOMAIN` | Configurable | Base domain (default: `cognitive-os.org`) |
| `R2_ENDPOINT` | Cloudflare R2 dashboard | `https://<account-id>.r2.cloudflarestorage.com` |
| `R2_BUCKET` | Cloudflare R2 dashboard | R2 bucket name (default: `cognitiveos-registry`) |
| `R2_ACCESS_KEY` | Cloudflare R2 API tokens | Access Key ID |
| `R2_SECRET_KEY` | Cloudflare R2 API tokens | Secret Access Key |
| `REGISTRY_GH_TOKEN` | GitHub Settings → Developer settings | Classic PAT with `repo` scope |
| `REGISTRY_GH_ORG` | GitHub | Target org name (e.g., `CognitiveOS-CGP-Packages`) |

**Clean up the local key file after adding to GitHub:**

```bash
rm /tmp/registry-deployer-key.json
```

#### Step 4: Deploy

Push to `main` triggers automatic deployment:

```bash
git push origin main
```

Or deploy manually:

```bash
gcloud run deploy registry-server \
  --source . \
  --platform managed \
  --region us-central1 \
  --min-instances 0 \
  --max-instances 10 \
  --port 8080
```

Free tier: 240,000 vCPU-seconds and 450,000 GiB-seconds per month.

### Local Development

```bash
make build
./build/bin/registry-server -data-dir ./data
```

## Storage

- In-memory store (default)
- File-backed store with `-sqlite` flag (writes to `DATA_DIR/patches.json`)
- S3-compatible store via `S3_*` env vars (Cloudflare R2 default)

## Middleware Chain

```
Request → CORS → AntiBot → RateLimit → Auth (per-route) → Handler
```

## Architecture

- S3-compatible interface (Cloudflare R2 default, configurable via `S3_*` env vars)
- SSH public key authentication for publishers (SSHSIG protocol)
- Notary checksum model for integrity verification

See [ADR-007](https://github.com/CognitiveOS-Project/product-specs/blob/main/adr/ADR-007-registry-server-architecture.md) and [ADR-008](https://github.com/CognitiveOS-Project/product-specs/blob/main/adr/ADR-008-hosting-decision.md).

## Related

- [CognitiveOS](https://github.com/CognitiveOS-Project/cognitiveos) — main project repository
- [cognitive-os.org](https://cognitive-os.org) — project website
- [cpm](https://github.com/CognitiveOS-Project/cpm) — CLI client that searches and downloads from this registry
- [coginit](https://github.com/CognitiveOS-Project/coginit) — boot manager that orchestrates CognitiveOS services
- [Product Specs](https://github.com/CognitiveOS-Project/product-specs) — registry API specification
- [CognitiveOS Project](https://github.com/CognitiveOS-Project) — GitHub organization

## Contributing

1. Branch from `main`
2. Use topic branches: `feature/<name>`, `fix/<name>`
3. Open a PR to `main` with a clear title and description
4. Merge after review

See the [SDLC repo](https://github.com/CognitiveOS-Project/sdlc) for the full contribution guide, code review standards, and testing strategy.

## Author

**Jean Machuca** — [GitHub](https://github.com/jeanmachuca) · [Sponsor](https://github.com/sponsors/jeanmachuca)

## License

MIT
