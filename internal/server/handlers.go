package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/CognitiveOS-Project/registry-server/internal/store"
	"github.com/CognitiveOS-Project/registry-server/internal/validate"
)

func (s *Server) handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		patchesCount, _ := s.config.Store.List()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":        "healthy",
			"patches_count": len(patchesCount),
			"version":       "1.1.0",
		})
	}
}

func (s *Server) handleSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		results, err := s.config.Store.Search(q)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, results)
	}
}

func (s *Server) handleGetPatch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		version := r.PathValue("version")

		pkg, err := s.config.Store.Get(name, version)
		if err != nil {
			if err == store.ErrNotFound {
				writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
					fmt.Sprintf("package '%s' version '%s' not found", name, version))
				return
			}
			writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, pkg)
	}
}

func (s *Server) handleGetVersions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		versions, err := s.config.Store.Versions(name)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		if len(versions) == 0 {
			writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
				fmt.Sprintf("package '%s' not found", name))
			return
		}

		type verEntry struct {
			Version     string `json:"version"`
			PublishedAt string `json:"published_at"`
			SHA256      string `json:"sha256"`
			DownloadURL string `json:"download_url"`
			Status      string `json:"status"`
		}
		entries := make([]verEntry, len(versions))
		for i, v := range versions {
			entries[i] = verEntry{
				Version:     v.Version,
				PublishedAt: v.CreatedAt,
				SHA256:      v.SHA256,
				DownloadURL: v.DownloadURL,
				Status:      v.Status,
			}
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"name":     name,
			"versions": entries,
		})
	}
}

func (s *Server) handleDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		version := r.PathValue("version")

		pkg, err := s.config.Store.Get(name, version)
		if err != nil {
			if err == store.ErrNotFound {
				writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
					fmt.Sprintf("package '%s' version '%s' not found", name, version))
				return
			}
			writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}

		if pkg.DownloadURL == "" {
			writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
				"no download URL registered for this package")
			return
		}

		s.config.Store.IncrementDownloads(name, version)
		http.Redirect(w, r, pkg.DownloadURL, http.StatusFound)
	}
}

type publishRequest struct {
	Name             string          `json:"name"`
	Version          string          `json:"version"`
	Description      string          `json:"description,omitempty"`
	Author           string          `json:"author,omitempty"`
	License          string          `json:"license,omitempty"`
	SourceRepository string          `json:"source_repository,omitempty"`
	SourceIssues     string          `json:"source_issues,omitempty"`
	DownloadURL      string          `json:"download_url"`
	SHA256           string          `json:"sha256"`
	Tags             []string        `json:"tags,omitempty"`
	Manifest         json.RawMessage `json:"manifest"`
}

func (s *Server) handlePublish() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.publishPackage("", w, r)
	}
}

func (s *Server) handlePutVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		urlName := r.PathValue("name")
		urlVersion := r.PathValue("version")
		s.publishPackage(urlName+"/"+urlVersion, w, r)
	}
}

func (s *Server) publishPackage(urlPath string, w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"invalid JSON: "+err.Error())
		return
	}

	if req.Name == "" || req.Version == "" {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"name and version are required")
		return
	}

	if req.DownloadURL == "" {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"download_url is required (notary registry does not host files)")
		return
	}

	if req.SHA256 == "" {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"sha256 is required")
		return
	}

	if req.Manifest == nil || string(req.Manifest) == "null" || string(req.Manifest) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"manifest is required")
		return
	}

	// If URL path has name@version, validate match
	if urlPath != "" {
		parts := split2(urlPath, "/")
		if len(parts) == 2 {
			if parts[0] != req.Name {
				writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
					fmt.Sprintf("name mismatch: URL has '%s', body has '%s'", parts[0], req.Name))
				return
			}
			if parts[1] != req.Version {
				writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
					fmt.Sprintf("version mismatch: URL has '%s', body has '%s'", parts[1], req.Version))
				return
			}
		}
	}

	if _, err := s.config.Store.Get(req.Name, req.Version); err == nil {
		writeErrorJSON(w, http.StatusConflict, "ALREADY_EXISTS",
			fmt.Sprintf("package '%s' version '%s' already exists", req.Name, req.Version))
		return
	}

	// A1-A10 validation (archive=nil because JSON-only publish)
	vr := validate.Run(req.Manifest, nil, req.SHA256)
	if !vr.AllPassed() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"error": map[string]interface{}{
				"code":    "VALIDATION_FAILED",
				"message": vr.Error(),
				"details": vr.Errors,
			},
		})
		return
	}

	// A5: check dependencies exist
	var m struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(req.Manifest, &m); err == nil {
		for depName := range m.Dependencies {
			versions, err := s.config.Store.Versions(depName)
			if err != nil || len(versions) == 0 {
				writeErrorJSON(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED",
					fmt.Sprintf("A5: unresolvable dependency '%s' — not found in registry", depName))
				return
			}
		}

		// A6: no buggy deps
		for depName := range m.Dependencies {
			versions, _ := s.config.Store.Versions(depName)
			for _, v := range versions {
				if v.Status == "buggy" {
					writeErrorJSON(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED",
						fmt.Sprintf("A6: depends on buggy package '%s' version '%s'", depName, v.Version))
					return
				}
			}
		}
	}

	pkg := store.Package{
		Name:             req.Name,
		Version:          req.Version,
		Description:      req.Description,
		Author:           req.Author,
		License:          req.License,
		SourceRepository: req.SourceRepository,
		SourceIssues:     req.SourceIssues,
		DownloadURL:      req.DownloadURL,
		SHA256:           req.SHA256,
		Tags:             req.Tags,
		Manifest:         string(req.Manifest),
	}

	if err := s.config.Store.Put(pkg); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	log.Printf("Notary: registered %s v%s (sha256=%s)", req.Name, req.Version, pkg.SHA256)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"name":         req.Name,
		"version":      req.Version,
		"sha256":       pkg.SHA256,
		"download_url": pkg.DownloadURL,
		"url":          fmt.Sprintf("/v1/patches/%s/%s", req.Name, req.Version),
	})
}

func (s *Server) handleSetStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		version := r.PathValue("version")

		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid JSON: "+err.Error())
			return
		}

		validStatuses := map[string]bool{"active": true, "deprecated": true, "buggy": true}
		if !validStatuses[req.Status] {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid status; must be one of: active, deprecated, buggy")
			return
		}

		if err := s.config.Store.SetStatus(name, version, req.Status); err != nil {
			if err == store.ErrNotFound {
				writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
					fmt.Sprintf("package '%s' version '%s' not found", name, version))
				return
			}
			writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}

		log.Printf("Status: %s v%s → %s", name, version, req.Status)
		writeJSON(w, http.StatusOK, map[string]string{"status": req.Status})
	}
}

func (s *Server) handleGetDependencies() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		version := r.URL.Query().Get("version")

		if version == "" {
			versions, err := s.config.Store.Versions(name)
			if err != nil || len(versions) == 0 {
				writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
					fmt.Sprintf("package '%s' not found", name))
				return
			}
			version = versions[0].Version
		}

		pkg, err := s.config.Store.Get(name, version)
		if err != nil {
			writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
				fmt.Sprintf("package '%s' version '%s' not found", name, version))
			return
		}

		var deps map[string]string
		if pkg.Manifest != "" {
			var m struct {
				Dependencies map[string]string `json:"dependencies"`
			}
			if err := json.Unmarshal([]byte(pkg.Manifest), &m); err == nil {
				deps = m.Dependencies
			}
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"name":         name,
			"version":      version,
			"dependencies": deps,
			"status":       pkg.Status,
		})
	}
}

func (s *Server) handleValidate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		version := r.PathValue("version")

		pkg, err := s.config.Store.Get(name, version)
		if err != nil {
			writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
				fmt.Sprintf("package '%s' version '%s' not found", name, version))
			return
		}

		manifest := json.RawMessage(pkg.Manifest)
		if len(manifest) == 0 || string(manifest) == "" {
			writeErrorJSON(w, http.StatusUnprocessableEntity, "INTERNAL_ERROR",
				"no manifest stored for validation")
			return
		}

		vr := validate.Run(manifest, nil, pkg.SHA256)
		rules := make(map[string]bool)
		for rule := range vr.Passed {
			rules[rule] = true
		}
		for _, e := range vr.Errors {
			rules[e.Rule] = false
		}

		status := "valid"
		if !vr.AllPassed() {
			status = "invalid"
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": status,
			"rules":  rules,
		})
	}
}

func (s *Server) handleUnlock() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model      string `json:"model"`
			UnlockCode string `json:"unlock_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid JSON: "+err.Error())
			return
		}

		if req.Model == "" || req.UnlockCode == "" {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"model and unlock_code are required")
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "ok",
			"model":   req.Model,
			"message": "model unlocked successfully",
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErrorJSON(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func split2(s, sep string) []string {
	for i := 0; i < len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			return []string{s[:i], s[i+len(sep):]}
		}
	}
	return []string{s}
}
