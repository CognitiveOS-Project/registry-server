#!/usr/bin/env bash
# Setup Cloudflare R2 bucket for registry-server storage
# Usage: ./scripts/cloudflare/setup-r2.sh
set -euo pipefail

echo "=== Cloudflare R2 Setup ==="
echo ""
echo "This script guides you through creating an R2 bucket and API token."
echo "You'll need to do most steps in the Cloudflare dashboard."
echo ""

read -p "Enter bucket name [registry-server]: " BUCKET_NAME
BUCKET_NAME="${BUCKET_NAME:-registry-server}"

# Check if rclone is installed (optional helper)
if command -v rclone &> /dev/null; then
    echo "rclone detected — can verify bucket connectivity."
else
    echo "rclone not found (optional). Install for bucket verification."
    echo ""
fi

echo "=== Step 1: Create R2 Bucket ==="
echo ""
echo "1. Go to: https://dash.cloudflare.com → R2 Object Storage"
echo "2. Click 'Create bucket'"
echo "3. Bucket name: $BUCKET_NAME"
echo "4. Location: Auto (or closest to your users)"
echo "5. Storage class: Standard"
echo "6. Click 'Create bucket'"
echo ""
read -p "Press Enter when bucket is created..."

echo ""
echo "=== Step 2: Configure Bucket Public Access ==="
echo ""
echo "1. In the bucket settings, go to 'Settings'"
echo "2. Enable 'Public Access' for notary metadata reads"
echo "3. This allows cpm clients to verify package checksums"
echo ""
read -p "Press Enter when public access is configured..."

echo ""
echo "=== Step 3: Create API Token ==="
echo ""
echo "1. Go to: https://dash.cloudflare.com → R2 → Manage R2 API Tokens"
echo "2. Click 'Create API token'"
echo "3. Token name: registry-server-deployer"
echo "4. Permissions: Object Read & Write"
echo "5. Specify buckets: $BUCKET_NAME"
echo "6. Click 'Create API Token'"
echo ""

echo "=== Step 4: Get Endpoint URL ==="
echo ""
echo "1. Go to: R2 → Account ID (shown in dashboard URL)"
echo "2. Endpoint format: https://<ACCOUNT_ID>.r2.cloudflarestorage.com"
echo ""

read -p "Enter your R2 Account ID: " ACCOUNT_ID
R2_ENDPOINT="https://$ACCOUNT_ID.r2.cloudflarestorage.com"

echo ""
echo "=== Step 5: Note Your Credentials ==="
echo ""
echo "After creating the API token, you'll see:"
echo "  - Access Key ID"
echo "  - Secret Access Key"
echo ""
echo "Save these — they won't be shown again."

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Add these GitHub secrets to your registry-server repository:"
echo ""
echo "  R2_ENDPOINT  = $R2_ENDPOINT"
echo "  R2_BUCKET    = $BUCKET_NAME"
echo "  R2_ACCESS_KEY = <your Access Key ID>"
echo "  R2_SECRET_KEY = <your Secret Access Key>"
echo ""
echo "Registry URL will be:"
echo "  https://registry-us-all-distros-official.<BASE_DOMAIN>/v1"
