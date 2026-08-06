package lmchatkit

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const defaultMaxUploadBytes = 10 << 20 // 10 MB

// uploadResponse is the JSON returned by POST /api/upload for each file.
type uploadResponse struct {
	Files []uploadedFile `json:"files"`
}

type uploadedFile struct {
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	DataURL  string `json:"data_url"`
	Size     int64  `json:"size"`
}

// handleConfig returns feature flags the frontend needs to adapt its UI.
// Lightweight — no auth-sensitive data, just booleans.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg := map[string]interface{}{
		"file_upload": s.cfg.FileUpload,
	}
	writeJSON(w, http.StatusOK, cfg)
}

// handleUpload accepts multipart/form-data file uploads and returns
// base64-encoded data URLs suitable for passing as image_url content blocks
// to the LLM. Only mounted when Config.FileUpload is true.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	maxBytes := s.cfg.MaxUploadBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxUploadBytes
	}

	// Limit the total request body.
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1024) // +1KB for multipart headers

	if err := r.ParseMultipartForm(maxBytes); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	defer r.MultipartForm.RemoveAll()

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no files uploaded")
		return
	}

	result := uploadResponse{Files: make([]uploadedFile, 0, len(files))}

	for _, fh := range files {
		if fh.Size > maxBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large: "+fh.Filename)
			return
		}

		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read file")
			return
		}

		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read file")
			return
		}

		// Detect MIME type from the file header Content-Type, fall back to
		// http.DetectContentType on the first 512 bytes.
		mimeType := fh.Header.Get("Content-Type")
		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = http.DetectContentType(data)
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		dataURL := "data:" + mimeType + ";base64," + encoded

		result.Files = append(result.Files, uploadedFile{
			Name:     fh.Filename,
			MimeType: mimeType,
			DataURL:  dataURL,
			Size:     fh.Size,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}
