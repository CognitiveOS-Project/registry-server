# registry-server

CognitiveOS .cgp package registry — a Go HTTP server for hosting, searching, versioning, and distributing cognitive patches with license/code unlock support.

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/search?q=` | Search patches |
| GET | `/v1/patches/:name` | Get patch metadata |
| GET | `/v1/patches/:name/download` | Download .cgp archive |
| POST | `/v1/patches` | Publish new patch |
| PUT | `/v1/patches/:name/version` | Publish new version |
| GET | `/health` | Healthcheck |

## Authentication

- **Public:** Read access for search, metadata, and download
- **Token-based:** Publishing requires a valid token
- **Code unlock:** Paid/supporter-only patches use unlock codes

## Build

```bash
go build -o bin/registry-server ./cmd/registry-server
```

## Storage

- Filesystem-backed metadata store
- Configurable patches directory

## Related

- [CognitiveOS](https://github.com/CognitiveOS-Project/cognitiveos) — main project repository
- [cognitive-os.org](https://cognitive-os.org) — project website
- [cpm](https://github.com/CognitiveOS-Project/cpm) — CLI client that searches and downloads from this registry
- [Product Specs](https://github.com/CognitiveOS-Project/product-specs) — registry API specification
- [CognitiveOS Project](https://github.com/CognitiveOS-Project) — GitHub organization

## Author

**Jean Machuca** — [GitHub](https://github.com/jeanmachuca) · [Sponsor](https://github.com/sponsors/jeanmachuca)

## License

MIT
