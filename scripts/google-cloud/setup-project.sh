#!/usr/bin/env bash
# Setup Google Cloud project for registry-server deployment
# Usage: ./scripts/google-cloud/setup-project.sh
set -euo pipefail

echo "=== Google Cloud Project Setup ==="
echo ""

# Check if gcloud is installed
if ! command -v gcloud &> /dev/null; then
    echo "Error: gcloud CLI not found."
    echo "Install: https://cloud.google.com/sdk/docs/install"
    exit 1
fi

# Check if authenticated
if ! gcloud auth list --filter="status:ACTIVE" --format="value(account)" | grep -q .; then
    echo "Not authenticated. Running: gcloud auth login"
    gcloud auth login
fi

# Get project ID
read -p "Enter GCP project ID (or press Enter to use current): " PROJECT_ID
if [ -z "$PROJECT_ID" ]; then
    PROJECT_ID=$(gcloud config get-value project 2>/dev/null)
    if [ -z "$PROJECT_ID" ]; then
        echo "No project set. Please provide one."
        exit 1
    fi
fi

echo "Using project: $PROJECT_ID"
gcloud config set project "$PROJECT_ID"

# Enable required APIs
echo ""
echo "Enabling required APIs..."
gcloud services enable run.googleapis.com
gcloud services enable containerregistry.googleapis.com
gcloud services enable cloudbuild.googleapis.com

echo "APIs enabled."

# Create service account
SA_NAME="registry-deployer"
SA_EMAIL="$SA_NAME@$PROJECT_ID.iam.gserviceaccount.com"

echo ""
echo "Creating service account: $SA_NAME"
gcloud iam service-accounts create "$SA_NAME" \
    --display-name="Registry Server Deployer" \
    --description="Deployer for registry-server to Cloud Run" \
    2>/dev/null || echo "Service account already exists."

# Grant roles
echo ""
echo "Granting roles..."
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member="serviceAccount:$SA_EMAIL" \
    --role="roles/run.admin" \
    --quiet

gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member="serviceAccount:$SA_EMAIL" \
    --role="roles/storage.admin" \
    --quiet

gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member="serviceAccount:$SA_EMAIL" \
    --role="roles/artifactregistry.writer" \
    --quiet

echo "Roles granted."

# Generate JSON key
KEY_FILE="/tmp/registry-deployer-key.json"
echo ""
echo "Generating JSON key..."
gcloud iam service-accounts keys create "$KEY_FILE" \
    --iam-account="$SA_EMAIL"

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Add these GitHub secrets to your registry-server repository:"
echo ""
echo "  GCP_PROJECT_ID = $PROJECT_ID"
echo "  GCP_SA_KEY     = (contents of $KEY_FILE)"
echo ""
echo "To get the key content:"
echo "  cat $KEY_FILE"
echo ""
echo "Delete the key file after adding to GitHub secrets:"
echo "  rm $KEY_FILE"
