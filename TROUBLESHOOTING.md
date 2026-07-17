# Troubleshooting

## Custom Domain Setup

### Architecture

```
Client → Cloudflare (DNS only) → Cloud Run (TLS + app)
```

The registry server runs on Google Cloud Run. A custom domain maps to it via Cloud Run domain mappings, which provision a Google-managed SSL certificate. Cloudflare provides DNS resolution only (grey cloud / DNS-only mode).

### Domain Naming Convention

Official registry URLs follow this pattern:

```
https://registry-{country}-{distro}-{role}.{BASE_DOMAIN}/v1
```

**Example:** `https://registry-us-all-distros-official.cognitive-os.org/v1`

### Why Cloudflare Proxy (Orange Cloud) Doesn't Work

Cloudflare Universal SSL certificates only cover **one level** of subdomain:

| Hostname | Covered by `*.cognitive-os.org`? |
|----------|----------------------------------|
| `registry.cognitive-os.org` | Yes |
| `registry-us-all-distros-official.cognitive-os.org` | Yes (one level) |
| `registry-us-all-distros-official.registry.cognitive-os.org` | **No** (two levels) |

Multi-level subdomains require either:
- **Enterprise SSL** on Cloudflare (paid)
- **Total TLS** on Cloudflare (requires Advanced Certificate Manager)
- **Cloudflare Origin Rules** Host header override (Enterprise only)
- **Cloudflare Worker** to rewrite the Host header (free, but adds runtime)
- **Cloud Run domain mapping** (recommended — see below)

### Cloudflare Origin Rules Host Header Override

Enterprise only. Cannot override the `Host` header on Free/Pro/Business plans.

### Cloudflare Transform Rules

Cloudflare blocks `Host` header modifications via Transform Rules. Error: `'set' is not a valid value for operation because it cannot be used on header 'Host'`.

### Cloudflare Worker Alternative

A Worker can proxy requests and rewrite the Host header. However, Cloud Run domain mappings are simpler and don't require code.

### Cloudflare Proxy + CNAME to Cloud Run URL

When Cloudflare proxies a CNAME to `*.run.app`, it sends the custom domain as the `Host` header. Cloud Run rejects requests with unrecognized hostnames (returns 404 from Google frontend). The Host header must match a mapped domain or the service's default URL.

## Cloud Run Domain Mapping

### Prerequisites

1. **`domains.cloudrun.com` API** must be enabled:
   ```bash
   gcloud services enable domains.cloudrun.com
   ```

2. **Service account** needs `roles/run.admin` and `roles/iam.serviceAccountUser`.

### Create the Mapping

```bash
gcloud beta run domain-mappings create \
  --service=registry-server \
  --domain=registry-us-all-distros-official.cognitive-os.org \
  --region=us-central1
```

Output:
```
NAME                              RECORD TYPE  CONTENTS
registry-us-all-distros-official  CNAME        ghs.googlehosted.com.
```

### Update DNS

In Cloudflare DNS → Records:
- **Type:** CNAME
- **Name:** `registry-us-all-distros-official`
- **Target:** `ghs.googlehosted.com`
- **Proxy:** **DNS only** (grey cloud)

Cloudflare must NOT proxy (orange cloud) because Google manages TLS directly via the managed SSL certificate.

### SSL Certificate Provisioning

Google provisions a managed SSL certificate automatically after DNS is configured. This takes **10-30 minutes**.

During provisioning:
- TLS handshake fails with `unexpected eof while reading`
- The domain resolves to `ghs.googlehosted.com` correctly
- The certificate is not yet installed on the load balancer

After provisioning:
- TLS handshake succeeds
- Certificate is valid and auto-renewed
- Health endpoint responds normally

### Verify

```bash
curl -sf https://registry-us-all-distros-official.cognitive-os.org/v1/health
```

Expected: `{"patches_count":0,"status":"healthy","version":"1.1.0"}`

## DNS Propagation

After changing a CNAME in Cloudflare:
- **Cloudflare resolver (1.1.1.1):** Updates within seconds
- **Local DNS cache:** May take minutes to hours depending on TTL
- **Google DNS (8.8.8.8):** Usually propagates within minutes

To test with Cloudflare's resolver directly:
```bash
nslookup registry-us-all-distros-official.cognitive-os.org 1.1.1.1
```

To force a specific IP (bypass local DNS cache):
```bash
curl --resolve registry-us-all-distros-official.cognitive-os.org:443:<IP> https://registry-us-all-distros-official.cognitive-os.org/v1/health
```

## Docker Build Errors

### `.dockerignore` excludes scripts

Setup Dockerfiles (`Dockerfile-gcloud`, `Dockerfile-cloudflare`) need `scripts/` in the build context. If `scripts` is in `.dockerignore`, these builds fail with `COPY scripts/: not found`.

**Fix:** Remove `scripts` from `.dockerignore`. The app Dockerfile uses multi-stage build and only copies what it needs.

### Minimal base images missing bash

The `rclone/rclone` image is Alpine-based but doesn't include `bash`. Scripts with `#!/usr/bin/env bash` shebangs fail with `env: can't execute 'bash': No such file or directory`.

**Fix:** Add `RUN apk add --no-cache bash` to the Dockerfile.

## GCP IAM Permissions

### `artifactregistry.repositories.uploadArtifacts` denied

The deploy service account needs `roles/artifactregistry.writer` to push Docker images to Artifact Registry.

### `iam.serviceaccounts.actAs` denied

The deploy service account needs `roles/iam.serviceAccountUser` to deploy to Cloud Run (which uses the compute default service account).

### Required roles for deploy service account

```bash
gcloud projects add-iam-policy-binding PROJECT_ID \
  --member="serviceAccount:registry-deployer@PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/run.admin"

gcloud projects add-iam-policy-binding PROJECT_ID \
  --member="serviceAccount:registry-deployer@PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/artifactregistry.writer"

gcloud projects add-iam-policy-binding PROJECT_ID \
  --member="serviceAccount:registry-deployer@PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/iam.serviceAccountUser"
```

## GCR vs Artifact Registry

Google Container Registry (`gcr.io`) is legacy. New projects may not have it enabled. Use Artifact Registry (`us-docker.pkg.dev`) instead.

| | GCR (legacy) | Artifact Registry |
|--|--------------|-------------------|
| URL pattern | `gcr.io/PROJECT_ID/image` | `us-docker.pkg.dev/PROJECT_ID/REPO/image` |
| API | `containerregistry.googleapis.com` | `artifactregistry.googleapis.com` |
| Status | Deprecated | Recommended |

## Related Docs

- [ADR-007](https://github.com/CognitiveOS-Project/product-specs/blob/main/adr/ADR-007-registry-server-architecture.md) — Architecture decisions
- [ADR-008](https://github.com/CognitiveOS-Project/product-specs/blob/main/adr/ADR-008-hosting-decision.md) — Hosting decision (Cloud Run)
- [Fair Use Policy](https://github.com/CognitiveOS-Project/product-specs/blob/main/specs/fair-use-policy.md) — Rate limits and acceptable use
