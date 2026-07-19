package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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

			var payload struct {
				Manifest json.RawMessage `json:"manifest"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid JSON body")
				return
			}

			if payload.Manifest == nil || string(payload.Manifest) == "null" || string(payload.Manifest) == "" {
				writeError(w, http.StatusBadRequest, "VALIDATION_FAILED", "manifest field is required for SSH-signed requests")
				return
			}

			hash := sha256.Sum256(payload.Manifest)

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

			next(w, r)
		}
	}
}
