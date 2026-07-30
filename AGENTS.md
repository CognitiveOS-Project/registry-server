# CognitiveOS Registry Server

The `.cgp` package registry — a Go HTTP server for hosting, searching, versioning, and distributing cognitive patches with license/code unlock support.

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | /v1/health | Healthcheck |
| GET | /v1/search?q= | Search patches |
| GET | /v1/patches/:name | Get patch metadata |
| GET | /v1/patches/:name/versions | List all versions |
| GET | /v1/patches/:name/:version | Get specific version |
| GET | /v1/patches/:name/:version/download | Download .cgp archive |
| GET | /v1/patches/:name/dependencies | Get dependency graph |
| GET | /v1/notary/check | Check notary checksum |
| POST | /v1/auth/register | Register SSH public key |
| POST | /v1/patches | Publish new patch |
| PUT | /v1/patches/:name/:version | Publish new version |
| PATCH | /v1/patches/:name/:version/status | Set version status (admin) |
| POST | /v1/patches/:name/:version/validate | Validate checksum (admin) |
| POST | /v1/patches/:name/:version/unlock | Unlock paid/supporter patch |

## Authentication

- Public read access for search, metadata, and download
- Token-based auth for publishing (legacy, still active)
- SSH key-based auth for publishers (SSHSIG protocol)
- Code-based unlock for paid/supporter patches

## Storage

- In-memory store (default)
- File-backed JSON store with `-file` flag
- S3-compatible store via `S3_*` env vars (Cloudflare R2 default)

## Middleware

- CORS (Allow-Origin: *)
- Anti-bot (User-Agent filtering, path probing protection, 1 MB size limit)
- Rate limiting (per-IP, token bucket)
- Per-route auth (publish/admin)

## Build

```bash
make build    # compile to build/bin/registry-server
make test     # run tests
make lint     # go vet
make clean    # remove build artifacts
```

## Deployment

- Google Cloud Run (primary, automated via GitHub Actions)
- Cloudflare R2 for S3-compatible storage
