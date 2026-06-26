package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/CognitiveOS-Project/registry-server/internal/store"
)

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

		pkg, err := s.config.Store.Get(name, version)
		if err != nil {
			if err == store.ErrNotFound {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "package not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		if pkg.DownloadURL == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no download URL registered for this package"})
			return
		}

		http.Redirect(w, r, pkg.DownloadURL, http.StatusFound)
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
			downloadURL := r.FormValue("download_url")
			tagsStr := r.FormValue("tags")

			if name == "" || version == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and version are required"})
				return
			}

			var tags []string
			if tagsStr != "" {
				tags = strings.Split(tagsStr, ",")
				for i := range tags {
					tags[i] = strings.TrimSpace(tags[i])
				}
			}

			pkg := store.Package{
				Name:        name,
				Version:     version,
				Description: description,
				Author:      author,
				DownloadURL: downloadURL,
				Tags:        tags,
			}

			file, _, err := r.FormFile("file")
			if err == nil {
				defer file.Close()
				hasher := sha256.New()
				size, err := io.Copy(hasher, file)
				if err == nil {
					pkg.Size = size
					pkg.SHA256 = hex.EncodeToString(hasher.Sum(nil))
				}
			}

			if err := s.config.Store.Put(pkg); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}

			log.Printf("Notary: registered %s v%s (sha256=%s)", name, version, pkg.SHA256)
			writeJSON(w, http.StatusCreated, pkg)
			return
		}

		var req struct {
			Name        string   `json:"name"`
			Version     string   `json:"version"`
			Description string   `json:"description"`
			Author      string   `json:"author"`
			DownloadURL string   `json:"download_url"`
			SHA256      string   `json:"sha256"`
			Tags        []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}

		if req.Name == "" || req.Version == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and version are required"})
			return
		}

		pkg := store.Package{
			Name:        req.Name,
			Version:     req.Version,
			Description: req.Description,
			Author:      req.Author,
			DownloadURL: req.DownloadURL,
			SHA256:      req.SHA256,
			Tags:        req.Tags,
		}

		if err := s.config.Store.Put(pkg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		log.Printf("Notary: registered %s v%s (sha256=%s)", req.Name, req.Version, pkg.SHA256)
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
