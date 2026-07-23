package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	"github.com/CognitiveOS-Project/registry-server/internal/store"
	"github.com/CognitiveOS-Project/registry-server/internal/validate"
	"golang.org/x/crypto/ssh"
)

func (s *Server) handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		patchesCount, _ := s.config.Store.List()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":        "healthy",
			"uptime_seconds": int(time.Since(s.startedAt).Seconds()),
			"patches_count": len(patchesCount),
			"version":       "2.1.0",
		})
	}
}

func (s *Server) handleSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")

		opts := store.SearchOptions{
			License:    r.URL.Query().Get("license"),
			Capability: r.URL.Query().Get("capability"),
			Author:     r.URL.Query().Get("author"),
			Exact:      r.URL.Query().Get("exact") == "true",
		}
		if v := r.URL.Query().Get("min_ram_mb"); v != "" {
			opts.MinRAM, _ = strconv.Atoi(v)
		}
		if v := r.URL.Query().Get("min_storage_mb"); v != "" {
			opts.MinStorageMB, _ = strconv.Atoi(v)
		}
		if v := r.URL.Query().Get("page"); v != "" {
			opts.Page, _ = strconv.Atoi(v)
		}
		if v := r.URL.Query().Get("per_page"); v != "" {
			opts.PerPage, _ = strconv.Atoi(v)
		}

		results, total, err := s.config.Store.SearchFiltered(q, opts)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}

		if opts.Page < 1 {
			opts.Page = 1
		}
		if opts.PerPage < 1 {
			opts.PerPage = 20
		}

		type searchResult struct {
			Name                 string         `json:"name"`
			Version              string         `json:"version"`
			Description          string         `json:"description"`
			Author               string         `json:"author,omitempty"`
			License              string         `json:"license"`
			SHA256               string         `json:"sha256"`
			Downloads            int64          `json:"downloads"`
			HardwareRequirements *store.HardwareReqs `json:"hardware_requirements,omitempty"`
			Capabilities         []string       `json:"capabilities,omitempty"`
			PublishedAt          string         `json:"published_at,omitempty"`
		}
		items := make([]searchResult, len(results))
		for i, r := range results {
			items[i] = searchResult{
				Name:                 r.Name,
				Version:              r.Version,
				Description:          r.Description,
				Author:               r.Author,
				License:              r.License,
				SHA256:               r.ChecksumSHA256,
				Downloads:            r.Downloads,
				HardwareRequirements: r.Hardware,
				Capabilities:         r.Capabilities,
				PublishedAt:          r.CreatedAt,
			}
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"results":  items,
			"total":    total,
			"page":     opts.Page,
			"per_page": opts.PerPage,
		})
	}
}

func (s *Server) handleGetPatch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		version := r.PathValue("version")

		if version == "" {
			versions, err := s.config.Store.Versions(name)
			if err != nil || len(versions) == 0 {
				writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
					fmt.Sprintf("package '%s' not found", name))
				return
			}
			writeJSON(w, http.StatusOK, versions[0])
			return
		}

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

		type versionInfo struct {
			Version      string            `json:"version"`
			PublishedAt  string            `json:"published_at,omitempty"`
			SHA256       string            `json:"sha256,omitempty"`
			DownloadURLs map[string]string `json:"download_urls,omitempty"`
			Status       string            `json:"status"`
		}
		entries := make([]versionInfo, len(versions))
		for i, v := range versions {
			entries[i] = versionInfo{
				Version:      v.Version,
				PublishedAt:  v.CreatedAt,
				SHA256:       v.ChecksumSHA256,
				DownloadURLs: v.DownloadURLs,
				Status:       v.Status,
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

		var downloadURL string

		osParam := r.URL.Query().Get("os")
		archParam := r.URL.Query().Get("arch")

		if osParam != "" && archParam != "" && pkg.DownloadURLs != nil {
			variant := osParam + "/" + archParam
			downloadURL = pkg.DownloadURLs[variant]
		}

		if downloadURL == "" && pkg.DownloadURLs != nil {
			downloadURL = pkg.DownloadURLs[""]
		}

		if downloadURL == "" {
			downloadURL = pkg.DownloadURL
		}

		if downloadURL == "" {
			writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
				"no download URL registered for this package")
			return
		}

		_, _ = s.config.Store.IncrementDownloads(name, version)
		http.Redirect(w, r, downloadURL, http.StatusFound)
	}
}

type publishRequest struct {
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Description      string            `json:"description,omitempty"`
	Author           string            `json:"author,omitempty"`
	License          string            `json:"license,omitempty"`
	SourceRepository string            `json:"source_repository,omitempty"`
	SourceIssues     string            `json:"source_issues,omitempty"`
	DownloadURL      string            `json:"download_url,omitempty"`           // v1 compat
	DownloadURLs     map[string]string `json:"download_urls,omitempty"`          // v2: variant-keyed
	SHA256           string            `json:"sha256"`
	Tags             []string          `json:"tags,omitempty"`
	Capabilities     []string          `json:"capabilities,omitempty"`
	Manifest         json.RawMessage   `json:"manifest"`
	Hardware         *store.HardwareReqs `json:"hardware_requirements,omitempty"`
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
	if isMultipart(r.Header.Get("Content-Type")) {
		s.publishOfficial(urlPath, w, r)
	} else {
		s.publishProxy(urlPath, w, r)
	}
}

func isMultipart(ct string) bool {
	mediaType, _, err := mime.ParseMediaType(ct)
	return err == nil && mediaType == "multipart/form-data"
}

func (s *Server) publishProxy(urlPath string, w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"invalid JSON: "+err.Error())
		return
	}
	s.processPublish(urlPath, req, w, r)
}

func (s *Server) publishOfficial(urlPath string, w http.ResponseWriter, r *http.Request) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid content type")
		return
	}
	boundary := params["boundary"]
	if boundary == "" {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED", "missing multipart boundary")
		return
	}

	mr := multipart.NewReader(r.Body, boundary)

	var metadataJSON []byte
	var cgpData []byte

	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}

		data, err := io.ReadAll(part)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				fmt.Sprintf("failed to read part %q", part.FormName()))
			return
		}

		switch part.FormName() {
		case "metadata":
			metadataJSON = data
		case "cgp":
			cgpData = data
		}
	}

	if metadataJSON == nil {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"metadata field is required")
		return
	}

	if len(cgpData) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"cgp field is required for official publish")
		return
	}

	if s.config.GitHub == nil || !s.config.GitHub.Enabled() {
		writeErrorJSON(w, http.StatusServiceUnavailable, "GITHUB_NOT_CONFIGURED",
			"official publish requires GitHub integration (REGISTRY_GH_TOKEN not set)")
		return
	}

	var req publishRequest
	if err := json.Unmarshal(metadataJSON, &req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"invalid metadata JSON: "+err.Error())
		return
	}

	if req.Name == "" || req.Version == "" {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"name and version are required")
		return
	}

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

	if req.Manifest != nil && string(req.Manifest) != "null" && string(req.Manifest) != "" {
		if req.SHA256 == "" {
			hash := sha256.Sum256(cgpData)
			req.SHA256 = hex.EncodeToString(hash[:])
		}
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
	}

	result, err := s.config.GitHub.PublishPackage(req.Name, req.Version, req.Description, cgpData)
	if err != nil {
		log.Printf("GitHub publish failed: %v", err)
		writeErrorJSON(w, http.StatusInternalServerError, "GITHUB_ERROR",
			fmt.Sprintf("failed to publish to GitHub: %v", err))
		return
	}

	downloadURLs := map[string]string{"": result.DownloadURL}

	pkg := store.Package{
		Name:             req.Name,
		Version:          req.Version,
		Description:      req.Description,
		Author:           req.Author,
		License:          req.License,
		SourceRepository: req.SourceRepository,
		SourceIssues:     req.SourceIssues,
		DownloadURL:      result.DownloadURL,
		DownloadURLs:     downloadURLs,
		ChecksumSHA256:   req.SHA256,
		Tags:             req.Tags,
		Capabilities:     req.Capabilities,
		Manifest:         string(req.Manifest),
		Hardware:         req.Hardware,
	}

	if req.Manifest != nil {
		var manifest struct {
			UnlockCodes []string `json:"unlock_codes,omitempty"`
		}
		if err := json.Unmarshal(req.Manifest, &manifest); err == nil && len(manifest.UnlockCodes) > 0 {
			hashed := make([]string, 0, len(manifest.UnlockCodes))
			for _, code := range manifest.UnlockCodes {
				h := sha256.Sum256([]byte(code))
				hashed = append(hashed, hex.EncodeToString(h[:]))
			}
			pkg.UnlockCodes = hashed
		}
	}

	if err := s.config.Store.Put(pkg); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	log.Printf("Official: published %s v%s (github=%s, sha256=%s)", req.Name, req.Version, result.DownloadURL, pkg.ChecksumSHA256)

	publisherFingerprint := ""
	if fp, ok := auth.FingerprintFromContext(r.Context()); ok {
		publisherFingerprint = fp
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"name":                  req.Name,
		"version":               req.Version,
		"sha256":                pkg.ChecksumSHA256,
		"download_urls":         downloadURLs,
		"publisher_fingerprint": publisherFingerprint,
		"url":                   fmt.Sprintf("/v1/patches/%s/%s", req.Name, req.Version),
	})
}

func (s *Server) processPublish(urlPath string, req publishRequest, w http.ResponseWriter, r *http.Request) {
	if req.Name == "" || req.Version == "" {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"name and version are required")
		return
	}

	if len(req.DownloadURLs) == 0 && req.DownloadURL == "" {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"download_urls is required (notary registry does not host files)")
		return
	}

	if req.SHA256 == "" {
		writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
			"sha256 is required")
		return
	}

	if s.config.Owners != nil {
		if fp, ok := auth.FingerprintFromContext(r.Context()); ok {
			_, key, err := s.config.Owners.GetByKey(fp)
			if err != nil || key == nil {
				writeErrorJSON(w, http.StatusForbidden, "KEY_NOT_CLAIMED",
					"machine key is not linked to an owner. An owner must link this key through the web UI before publishing")
				return
			}
			if key.Status == "revoked" {
				writeErrorJSON(w, http.StatusForbidden, "KEY_REVOKED",
					"this machine key has been revoked by the owner")
				return
			}
			if !key.PublishPermission {
				writeErrorJSON(w, http.StatusForbidden, "PUBLISH_NOT_AUTHORIZED",
					"owner has not granted publish permission for this machine")
				return
			}
		}
	}

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

	if req.Manifest != nil && string(req.Manifest) != "null" && string(req.Manifest) != "" {
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
	}

	downloadURLs := req.DownloadURLs
	if downloadURLs == nil {
		downloadURLs = map[string]string{"": req.DownloadURL}
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
		DownloadURLs:     downloadURLs,
		ChecksumSHA256:   req.SHA256,
		Tags:             req.Tags,
		Capabilities:     req.Capabilities,
		Manifest:         string(req.Manifest),
		Hardware:         req.Hardware,
	}

	if req.Manifest != nil {
		var manifest struct {
			UnlockCodes []string `json:"unlock_codes,omitempty"`
		}
		if err := json.Unmarshal(req.Manifest, &manifest); err == nil && len(manifest.UnlockCodes) > 0 {
			hashed := make([]string, 0, len(manifest.UnlockCodes))
			for _, code := range manifest.UnlockCodes {
				h := sha256.Sum256([]byte(code))
				hashed = append(hashed, hex.EncodeToString(h[:]))
			}
			pkg.UnlockCodes = hashed
		}
	}

	if err := s.config.Store.Put(pkg); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	log.Printf("Notary: registered %s v%s (sha256=%s)", req.Name, req.Version, pkg.ChecksumSHA256)

	publisherFingerprint := ""
	if fp, ok := auth.FingerprintFromContext(r.Context()); ok {
		publisherFingerprint = fp
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"name":                  req.Name,
		"version":               req.Version,
		"sha256":                pkg.ChecksumSHA256,
		"download_urls":         pkg.DownloadURLs,
		"publisher_fingerprint": publisherFingerprint,
		"url":                   fmt.Sprintf("/v1/patches/%s/%s", req.Name, req.Version),
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

type dependencyResponse struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Dependencies map[string]string `json:"dependencies"`
	Transitive   []string          `json:"transitive"`
	Status       string            `json:"status"`
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

		if deps == nil {
			deps = map[string]string{}
		}

		transitive := s.resolveTransitive(deps, map[string]bool{})

		writeJSON(w, http.StatusOK, dependencyResponse{
			Name:         name,
			Version:      version,
			Dependencies: deps,
			Transitive:   transitive,
			Status:       pkg.Status,
		})
	}
}

func (s *Server) resolveTransitive(deps map[string]string, seen map[string]bool) []string {
	var result []string
	for depName := range deps {
		if seen[depName] {
			continue
		}
		seen[depName] = true

		versions, err := s.config.Store.Versions(depName)
		if err != nil || len(versions) == 0 {
			continue
		}

		resolvedVersion := versions[0].Version
		result = append(result, depName+"@"+resolvedVersion)

		depPkg, err := s.config.Store.Get(depName, resolvedVersion)
		if err != nil {
			continue
		}

		var subDeps map[string]string
		if depPkg.Manifest != "" {
			var m struct {
				Dependencies map[string]string `json:"dependencies"`
			}
			if err := json.Unmarshal([]byte(depPkg.Manifest), &m); err == nil {
				subDeps = m.Dependencies
			}
		}

		result = append(result, s.resolveTransitive(subDeps, seen)...)
	}
	return result
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

		vr := validate.Run(manifest, nil, pkg.ChecksumSHA256)
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
		name := r.PathValue("name")
		version := r.PathValue("version")

		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid JSON: "+err.Error())
			return
		}

		if req.Code == "" {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"code is required")
			return
		}

		pkg, err := s.config.Store.Get(name, version)
		if err != nil {
			writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
				fmt.Sprintf("package '%s' version '%s' not found", name, version))
			return
		}

		if len(pkg.UnlockCodes) == 0 {
			writeErrorJSON(w, http.StatusBadRequest, "NO_UNLOCK_REQUIRED",
				"this package does not require an unlock code")
			return
		}

		codeHash := sha256.Sum256([]byte(req.Code))
		codeHashHex := hex.EncodeToString(codeHash[:])

		for _, stored := range pkg.UnlockCodes {
			if stored == codeHashHex {
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"status":  "ok",
					"name":    name,
					"version": version,
					"message": "model unlocked successfully",
				})
				return
			}
		}

		writeErrorJSON(w, http.StatusForbidden, "INVALID_UNLOCK_CODE",
			"the unlock code is invalid")
	}
}

func (s *Server) handleAuthRegister() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
				"POST required")
			return
		}

		var req struct {
			PublicKey string `json:"public_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid JSON: "+err.Error())
			return
		}

		if req.PublicKey == "" {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"public_key is required")
			return
		}

		info, err := s.config.SSHKeys.Register(req.PublicKey)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid public key: "+err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"fingerprint":    info.Fingerprint,
			"public_key_type": info.KeyType,
			"comment":        info.Comment,
			"registered_at":  info.Registered.Format(time.RFC3339),
		})
	}
}

func (s *Server) handleAuthStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
				"PUT required")
			return
		}

		var req struct {
			Fingerprint string `json:"fingerprint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid JSON: "+err.Error())
			return
		}

		if req.Fingerprint == "" {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"fingerprint is required")
			return
		}

		info, err := s.config.SSHKeys.GetByFingerprint(req.Fingerprint)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"fingerprint": req.Fingerprint,
				"registered":  false,
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"fingerprint":   info.Fingerprint,
			"registered":    true,
			"registered_at": info.Registered.Format(time.RFC3339),
		})
	}
}

func (s *Server) handleAuthSignup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED",
				"POST required")
			return
		}

		var req struct {
			Profile   json.RawMessage `json:"profile"`
			PublicKey string          `json:"public_key"`
			Signature string          `json:"signature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid JSON: "+err.Error())
			return
		}

		if req.PublicKey == "" || req.Signature == "" || len(req.Profile) == 0 {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"profile, public_key, and signature are required")
			return
		}

		pubKeyObj, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid public key: "+err.Error())
			return
		}

		sigBytes, err := base64.RawStdEncoding.DecodeString(req.Signature)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"invalid signature encoding: "+err.Error())
			return
		}

		format, rest := auth.ReadSSHString(sigBytes)
		blob, _ := auth.ReadSSHString(rest)
		sig := &ssh.Signature{
			Format: string(format),
			Blob:   blob,
		}

		hash := sha256.Sum256(req.Profile)
		if err := pubKeyObj.Verify(hash[:], sig); err != nil {
			writeErrorJSON(w, http.StatusUnauthorized, "SIGNATURE_INVALID",
				"signature verification failed: "+err.Error())
			return
		}

		machineID := auth.MachineIDFromPublicKey(pubKeyObj)

		profile := &auth.MachineProfile{
			MachineID: machineID,
			Owner: auth.OwnerProfile{
				PublicKey:   req.PublicKey,
				Fingerprint: auth.Fingerprint(pubKeyObj),
			},
			SubmittedAt: time.Now().UTC(),
		}
		if err := json.Unmarshal(req.Profile, &profile.Hardware); err != nil {
			log.Printf("warning: failed to unmarshal hardware profile: %v", err)
		}

		status := &auth.MachineSignupStatus{
			MachineID:   machineID,
			Status:      "pending",
			SubmittedAt: time.Now().UTC(),
		}

		if s.config.Machines != nil {
			_ = s.config.Machines.SaveProfile(profile)
			_ = s.config.Machines.SaveStatus(status)
		}

		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"machine_id": machineID,
			"status":     status.Status,
		})
	}
}

func (s *Server) handleNotaryCheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source := r.URL.Query().Get("source")
		path := r.URL.Query().Get("path")
		version := r.URL.Query().Get("version")

		if source == "" || path == "" || version == "" {
			writeErrorJSON(w, http.StatusBadRequest, "VALIDATION_FAILED",
				"source, path, and version are required")
			return
		}

		name := path
		pkg, err := s.config.Store.Get(name, version)
		if err != nil {
			writeErrorJSON(w, http.StatusNotFound, "NOT_FOUND",
				fmt.Sprintf("package '%s' version '%s' not found", name, version))
			return
		}

		variant := ""
		if osParam := r.URL.Query().Get("os"); osParam != "" {
			if archParam := r.URL.Query().Get("arch"); archParam != "" {
				variant = osParam + "/" + archParam
			}
		}

		storedHash := pkg.ChecksumSHA256

		var downloadURL string
		if variant != "" && pkg.DownloadURLs != nil {
			downloadURL = pkg.DownloadURLs[variant]
		}
		if downloadURL == "" && pkg.DownloadURLs != nil {
			downloadURL = pkg.DownloadURLs[""]
		}
		if downloadURL == "" {
			downloadURL = pkg.DownloadURL
		}

		verified := false
		var remoteHash string
		var remoteETag string

		if downloadURL != "" {
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Head(downloadURL)
			if err == nil {
				defer resp.Body.Close()

				if etag := resp.Header.Get("ETag"); etag != "" {
					remoteETag = etag
				}

				if sha := resp.Header.Get("X-Checksum-Sha256"); sha != "" {
					remoteHash = sha
				} else if ct := resp.Header.Get("Content-SHA256"); ct != "" {
					remoteHash = ct
				}

				if storedHash != "" && remoteHash != "" {
					verified = storedHash == remoteHash
				} else if storedHash != "" && remoteETag != "" {
					normalized := strings.Trim(remoteETag, "\"")
					verified = storedHash == normalized
				} else {
					verified = resp.StatusCode >= 200 && resp.StatusCode < 400
				}
			}
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"verified":    verified,
			"name":        pkg.Name,
			"version":     pkg.Version,
			"variant":     variant,
			"stored_hash": storedHash,
			"remote_hash": remoteHash,
			"remote_etag": remoteETag,
			"checked_at":  time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErrorJSON(w http.ResponseWriter, status int, code, message string) {
	writeErrorJSONWithDetails(w, status, code, message, nil)
}

func writeErrorJSONWithDetails(w http.ResponseWriter, status int, code, message string, details interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	if details != nil {
		resp["error"].(map[string]interface{})["details"] = details
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func split2(s, sep string) []string {
	for i := 0; i < len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			return []string{s[:i], s[i+len(sep):]}
		}
	}
	return []string{s}
}
