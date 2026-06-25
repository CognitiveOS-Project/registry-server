package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/CognitiveOS-Project/registry-server/internal/store"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

func (s *Server) handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (s *Server) handleSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		results, err := s.config.Store.Search(q)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "package not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, pkg)
	}
}

func (s *Server) handleDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		version := r.PathValue("version")

		_, err := s.config.Store.Get(name, version)
		if err != nil {
			if err == store.ErrNotFound {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "package not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		cgpPath := s.cgpPath(name, version)
		f, err := os.Open(cgpPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "package file not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer f.Close()

		stat, _ := f.Stat()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename="+name+"-"+version+".cgp")
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		io.Copy(w, f)
	}
}

func (s *Server) handlePublish() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")

		if strings.HasPrefix(contentType, "multipart/form-data") {
			err := r.ParseMultipartForm(32 << 20)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart form: " + err.Error()})
				return
			}

			name := r.FormValue("name")
			version := r.FormValue("version")
			description := r.FormValue("description")
			author := r.FormValue("author")
			sourceRepository := r.FormValue("source_repository")
			sourceIssues := r.FormValue("source_issues")
			tagsStr := r.FormValue("tags")

			if name == "" || version == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and version are required"})
				return
			}

			if sourceIssues != "" {
				if err := checkURL(sourceIssues); err != nil {
					writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid or unreachable issues URL: " + err.Error()})
					return
				}
			}

			var tags []string
			if tagsStr != "" {
				tags = strings.Split(tagsStr, ",")
				for i := range tags {
					tags[i] = strings.TrimSpace(tags[i])
				}
			}

			file, header, err := r.FormFile("file")
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file field required: " + err.Error()})
				return
			}
			defer file.Close()

			if err := os.MkdirAll(s.config.DataDir, 0755); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}

			cgpPath := s.cgpPath(name, version)
			dst, err := os.Create(cgpPath)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			defer dst.Close()

			size, err := io.Copy(dst, file)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}

			pkg := store.Package{
				Name:             name,
				Version:          version,
				Description:      description,
				Author:           author,
				SourceRepository: sourceRepository,
				SourceIssues:     sourceIssues,
				Size:             size,
				SHA256:           "",
				Downloads:        0,
				Tags:             tags,
			}

			if err := s.config.Store.Put(pkg); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}

			if _, err := s.config.Store.Get(name, version); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store package metadata"})
				return
			}

			if header != nil {
				log.Printf("Published %s v%s (%d bytes)", name, version, size)
			}
			writeJSON(w, http.StatusCreated, pkg)
			return
		}

		var req struct {
			Name             string   `json:"name"`
			Version          string   `json:"version"`
			Description      string   `json:"description"`
			Author           string   `json:"author"`
			SourceRepository string   `json:"source_repository"`
			SourceIssues     string   `json:"source_issues"`
			DownloadURL      string   `json:"download_url"`
			Tags             []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}

		if req.Name == "" || req.Version == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and version are required"})
			return
		}

		if req.SourceIssues != "" {
			if err := checkURL(req.SourceIssues); err != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid or unreachable issues URL: " + err.Error()})
				return
			}
		}

		pkg := store.Package{
			Name:             req.Name,
			Version:          req.Version,
			Description:      req.Description,
			Author:           req.Author,
			SourceRepository: req.SourceRepository,
			SourceIssues:     req.SourceIssues,
			Tags:             req.Tags,
		}

		if req.DownloadURL != "" {
			if err := os.MkdirAll(s.config.DataDir, 0755); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			cgpPath := s.cgpPath(req.Name, req.Version)
			if err := downloadFile(cgpPath, req.DownloadURL); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to download from URL: " + err.Error()})
				return
			}
			stat, err := os.Stat(cgpPath)
			if err == nil {
				pkg.Size = stat.Size()
			}
		}

		if err := s.config.Store.Put(pkg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusCreated, pkg)
	}
}

func (s *Server) handleUnlock() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model      string `json:"model"`
			UnlockCode string `json:"unlock_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}

		if req.Model == "" || req.UnlockCode == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model and unlock_code are required"})
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
	json.NewEncoder(w).Encode(v)
}

func downloadFile(path, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func checkURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("empty URL")
	}
	u, err := http.Get(rawURL)
	if err != nil {
		return fmt.Errorf("unreachable: %w", err)
	}
	u.Body.Close()
	if u.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", u.StatusCode)
	}
	return nil
}

