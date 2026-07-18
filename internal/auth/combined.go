package auth

import "net/http"

func RequirePublishAuth(tokenAuth TokenStore, sshKeys SSHKeyStore, requiredScopes ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-SSH-Fingerprint") != "" {
				RequireSSHAuth(sshKeys, requiredScopes...)(next)(w, r)
				return
			}

			RequireAuth(tokenAuth, requiredScopes...)(next)(w, r)
		}
	}
}
