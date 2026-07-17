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
| POST | `/v1/patches` | Publish new patch |
| PUT | `/v1/patches/:name/:version` | Publish new version |
| PATCH | `/v1/patches/:name/:version/status` | Set version status (admin) |
| POST | `/v1/patches/:name/:version/validate` | Validate checksum (admin) |
| POST | `/v1/patches/:name/:version/:code/unlock` | Unlock paid/supporter patch |

## Authentication

- **Public:** Read access for search, metadata, and download
- **Token-based:** Publishing requires a valid token
- **Code unlock:** Paid/supporter-only patches use unlock codes

## Rate Limiting

All endpoints are rate-limited per IP. Limits are intentionally restrictive:

| Endpoint | Limit |
|----------|-------|
| Read (search, metadata) | 10 req/min |
| Download | 5 req/min |
| Publish | 2 req/min |
| Unlock | 2 req/min |
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
3. **Request size limits** — 1 MB max body size

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Listen port |
| `DATA_DIR` | `./data` | Data directory for patches and metadata |

Command-line flags override env vars:

```bash
./registry-server -addr :9090 -data-dir /var/data -sqlite
```

## Build

```bash
make build    # Compile to build/bin/registry-server
make test     # Run tests
make lint     # Run go vet
make clean    # Remove build artifacts
```

## Docker

```bash
docker build -t registry-server .
docker run -p 8080:8080 registry-server
```

The Dockerfile uses a multi-stage build:
- **Build stage:** `golang:1.25` with `CGO_ENABLED=0` for a static binary
- **Runtime stage:** `gcr.io/distroless/static-debian12` (~10 MB image)

## Deployment

### Google Cloud Run (Primary)

Deployment is automated via GitHub Actions. Push to `main` triggers `deploy-cloud-run.yml`.

**Setup scripts:**

```bash
./scripts/google-cloud/setup-project.sh   # Create GCP project + service account
./scripts/cloudflare/setup-r2.sh           # Create R2 bucket + API token
```

**GitHub Secrets** (required for CI/CD):

| Secret | Source | Description |
|--------|--------|-------------|
| `GCP_PROJECT_ID` | GCP Console | Google Cloud project ID |
| `GCP_SA_KEY` | GCP IAM → Service Accounts → JSON key | Deploy authentication |
| `BASE_DOMAIN` | Configurable | Base domain (default: `cognitive-os.org`) |
| `R2_ENDPOINT` | Cloudflare R2 dashboard | S3-compatible endpoint (`https://<account-id>.r2.cloudflarestorage.com`) |
| `R2_BUCKET` | Cloudflare R2 dashboard | Bucket name (`cognitiveos-registry`) |
| `R2_ACCESS_KEY` | Cloudflare R2 API tokens | S3 access key |
| `R2_SECRET_KEY` | Cloudflare R2 API tokens | S3 secret key |

**Manual deploy:**

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
./build/bin/registry-server -sqlite -data-dir ./data
```

## Storage

- In-memory store (default)
- File-backed store with `-sqlite` flag (writes to `DATA_DIR/patches.json`)
- S3-compatible store via env vars (`S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_REGION`)

## Middleware Chain

```
Request → CORS → AntiBot → RateLimit → Auth (per-route) → Handler
```

## Storage (Implemented)

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
