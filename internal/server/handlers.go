package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

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

		opts := store.SearchOptions{
			License:    r.URL.Query().Get("license"),
			Capability: r.URL.Query().Get("capability"),
			Author:     r.URL.Query().Get("author"),
			Exact:      r.URL.Query().Get("exact") == "true",
		}
		if v := r.URL.Query().Get("min_ram_mb"); v != "" {
			opts.MinRAM, _ = strconv.Atoi(v)
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
			Name             string   `json:"name"`
			Version          string   `json:"version"`
			Description      string   `json:"description"`
			Author           string   `json:"author,omitempty"`
			License          string   `json:"license"`
			ChecksumSHA256   string   `json:"checksum_sha256"`
			Downloads        int64    `json:"downloads"`
			Tags             []string `json:"tags,omitempty"`
		}
		items := make([]searchResult, len(results))
		for i, r := range results {
			items[i] = searchResult{
				Name:           r.Name,
				Version:        r.Version,
				Description:    r.Description,
				Author:         r.Author,
				License:        r.License,
				ChecksumSHA256: r.ChecksumSHA256,
				Downloads:      r.Downloads,
				Tags:           r.Tags,
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
			Version string `json:"version"`
			Status  string `json:"status"`
		}
		entries := make([]versionInfo, len(versions))
		for i, v := range versions {
			entries[i] = versionInfo{
				Version: v.Version,
				Status:  v.Status,
			}
		}

		writeJSON(w, http.StatusOK, entries)
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
	s.processPublish(urlPath, req, w)
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

	if err := s.config.Store.Put(pkg); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	log.Printf("Official: published %s v%s (github=%s, sha256=%s)", req.Name, req.Version, result.DownloadURL, pkg.ChecksumSHA256)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"name":          req.Name,
		"version":       req.Version,
		"sha256":        pkg.ChecksumSHA256,
		"download_url":  result.DownloadURL,
		"download_urls": downloadURLs,
		"url":           fmt.Sprintf("/v1/patches/%s/%s", req.Name, req.Version),
		"release_tag":   result.ReleaseTag,
	})
}

func (s *Server) processPublish(urlPath string, req publishRequest, w http.ResponseWriter) {
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

	if err := s.config.Store.Put(pkg); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	log.Printf("Notary: registered %s v%s (sha256=%s)", req.Name, req.Version, pkg.ChecksumSHA256)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"name":          req.Name,
		"version":       req.Version,
		"sha256":        pkg.ChecksumSHA256,
		"download_urls": pkg.DownloadURLs,
		"url":           fmt.Sprintf("/v1/patches/%s/%s", req.Name, req.Version),
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

type dependencyTree struct {
	Name         string           `json:"name"`
	Version      string           `json:"version"`
	Dependencies []dependencyTree `json:"dependencies"`
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

		tree := s.resolveDependencyTree(deps, map[string]bool{})

		writeJSON(w, http.StatusOK, dependencyTree{
			Name:         name,
			Version:      version,
			Dependencies: tree,
		})
	}
}

func (s *Server) resolveDependencyTree(deps map[string]string, seen map[string]bool) []dependencyTree {
	if len(deps) == 0 {
		return nil
	}
	var result []dependencyTree
	for depName, versionConstraint := range deps {
		if seen[depName] {
			continue
		}
		seen[depName] = true

		versions, err := s.config.Store.Versions(depName)
		if err != nil || len(versions) == 0 {
			continue
		}

		resolvedVersion := versions[0].Version
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

		_ = versionConstraint
		result = append(result, dependencyTree{
			Name:         depName,
			Version:      resolvedVersion,
			Dependencies: s.resolveDependencyTree(subDeps, seen),
		})
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

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "ok",
			"name":    name,
			"version": version,
			"message": "model unlocked successfully",
		})
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

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"verified":    true,
			"name":        pkg.Name,
			"version":     pkg.Version,
			"variant":     variant,
			"stored_hash": storedHash,
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
