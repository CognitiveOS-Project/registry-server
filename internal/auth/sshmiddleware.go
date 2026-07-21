package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

func RequireSSHAuth(sshKeys SSHKeyStore, requiredScopes ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			fingerprint := r.Header.Get("X-SSH-Fingerprint")
			signature := r.Header.Get("X-SSH-Signature")

			if fingerprint == "" || signature == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing SSH auth headers (X-SSH-Fingerprint, X-SSH-Signature)")
				return
			}

			fingerprint = strings.TrimSpace(fingerprint)

			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_FAILED", "failed to read request body")
				return
			}
			r.Body.Close()

			r.Body = io.NopCloser(bytes.NewReader(body))

			manifestBytes, err := extractManifestBytes(body, r.Header.Get("Content-Type"))
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
				return
			}

			hash := sha256.Sum256(manifestBytes)

			if err := sshKeys.VerifySignature(fingerprint, signature, hash[:]); err != nil {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", fmt.Sprintf("invalid signature: %v", err))
				return
			}

			info, err := sshKeys.GetByFingerprint(fingerprint)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "key not found")
				return
			}

			if len(requiredScopes) > 0 {
				keyScope := info.Scope
				if keyScope == "" {
					keyScope = "publish"
				}
				if !hasScopes([]string{keyScope}, requiredScopes) {
					writeError(w, http.StatusForbidden, "FORBIDDEN",
						fmt.Sprintf("key requires scope(s): %s", strings.Join(requiredScopes, ", ")))
					return
				}
			}

			ctx := ContextWithFingerprint(r.Context(), fingerprint)
			next(w, r.WithContext(ctx))
		}
	}
}

func extractManifestBytes(body []byte, contentType string) ([]byte, error) {
	if strings.Contains(contentType, "multipart/form-data") {
		return extractManifestFromMultipart(body, contentType)
	}
	return extractManifestFromJSON(body)
}

func extractManifestFromJSON(body []byte) ([]byte, error) {
	var payload struct {
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid JSON body")
	}
	if payload.Manifest == nil || string(payload.Manifest) == "null" || string(payload.Manifest) == "" {
		return nil, fmt.Errorf("manifest field is required for SSH-signed requests")
	}
	return []byte(payload.Manifest), nil
}

func extractManifestFromMultipart(body []byte, contentType string) ([]byte, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("invalid content type")
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, fmt.Errorf("missing multipart boundary")
	}

	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, fmt.Errorf("metadata field not found in multipart body")
		}
		if part.FormName() == "metadata" {
			metadataBytes, err := io.ReadAll(part)
			if err != nil {
				return nil, fmt.Errorf("failed to read metadata part")
			}
			return extractManifestFromJSON(metadataBytes)
		}
	}
}
