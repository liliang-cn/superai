package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Uploading a file to import.
//
// The import used to take a path, and a path is only meaningful on the machine
// SuperAI runs on. In the desktop window those are the same machine and it was
// merely awkward; over the network they are not, and the field was unusable —
// a browser at a domain has no way to name a file on the server, and the
// obvious thing to type (a path on your own laptop) is the one thing that
// cannot work.
//
// So the file comes over the wire. It lands in the workspace's uploads
// directory, which is where the app already keeps files handed to it, and the
// import runs against that.

// uploadPath is the endpoint. Gated in auth.go like every other /api path.
const uploadPath = "/api/upload"

// maxUploadBytes caps a single upload.
//
// 64MB is far above any CSV or dump someone imports by hand and far below what
// would fill a disk by accident. The limit exists because the reader is
// otherwise happy to write until it runs out of room, and the failure would be
// the machine's, not the request's.
const maxUploadBytes = 64 << 20

// safeUploadName reduces a browser-supplied filename to a filename.
//
// The name comes from whoever is uploading, so nothing in it may be read as a
// path. filepath.Base is not enough on its own: it leaves "..", it does not
// touch Windows separators, and it returns "." for an empty name — all of
// which would go on to be joined onto a directory.
func safeUploadName(name string) string {
	name = strings.TrimSpace(name)
	// A Windows client sends backslashes, which Base does not treat as
	// separators on unix.
	name = strings.ReplaceAll(name, `\`, "/")
	name = filepath.Base(name)
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "..") {
		return "upload.dat"
	}
	return name
}

// handleUpload receives one file and reports where it landed.
//
// The path it returns is the server's, and it is what the caller passes back to
// the import — so the browser never has to know or guess one.
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "upload is POST only", http.StatusMethodNotAllowed)
		return
	}
	// Bounded before parsing, so an oversized body is refused rather than
	// buffered first and rejected after.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "could not read the upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file in the upload", http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	dir, err := a.uploadDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name := safeUploadName(hdr.Filename)
	dst := filepath.Join(dir, name)

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		http.Error(w, "could not write the file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	written, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil {
		// A partial file would import as a truncated table and look like the
		// data was wrong rather than the transfer.
		_ = os.Remove(dst)
		http.Error(w, "upload failed partway: "+copyErr.Error(), http.StatusInternalServerError)
		return
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		http.Error(w, "could not finish writing: "+closeErr.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"path":%q,"name":%q,"bytes":%d}`, dst, name, written)
}

// uploadDir is where uploads land: the workspace's uploads directory, the same
// one chat attachments use.
func (a *App) uploadDir() (string, error) {
	ws := a.workspaceDir()
	if ws == "" {
		return "", fmt.Errorf("workspace not configured")
	}
	dir := filepath.Join(ws, uploadsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create uploads dir: %w", err)
	}
	return dir, nil
}
