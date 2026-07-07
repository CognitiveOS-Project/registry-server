# CognitiveOS Registry Server

The `.cgp` package registry — a Go HTTP server for hosting, searching, and distributing cognitive patches.

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | /v1/search?q= | Search patches |
| GET | /v1/patches/:name | Get patch metadata |
| GET | /v1/patches/:name/download | Download .cgp archive |
| POST | /v1/patches | Publish new patch |
| PUT | /v1/patches/:name/version | Publish new version |

## Authentication

- Public read access for search and download
- Token-based auth for publishing
- Code-based unlock for paid/supporter patches

## Build

```bash
make build    # compile to build/bin/registry-server
make test     # run tests
make lint     # go vet
make clean    # remove build artifacts
```

## Storage

- Filesystem-backed (configurable `PATCHES_DIR`)
- SQLite metadata index
- Pluggable to S3-compatible storage for large-scale deployments
