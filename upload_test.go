package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func multipartBody(t *testing.T, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w.Close()
	return buf, w.FormDataContentType()
}

// The upload lands a file on the machine running SuperAI, so it has to be
// behind the same door as everything else. Forgetting this line would let
// anyone who can reach the port write files there.
func TestTheUploadEndpointIsGated(t *testing.T) {
	if !gatedPath(uploadPath) {
		t.Errorf("%s is not behind authentication", uploadPath)
	}
}

// A filename arrives from a browser and is chosen by whoever is uploading.
// Anything in it that reads as a path must not be honoured.
func TestUploadNamesCannotEscape(t *testing.T) {
	for _, name := range []string{
		"../../etc/passwd", "/etc/passwd", "a/b/c.csv", `..\..\win.ini`,
		".", "..", "", "   ",
	} {
		got := safeUploadName(name)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("%q became %q, which still contains a separator", name, got)
		}
		if got == "" || got == "." || got == ".." {
			t.Errorf("%q became %q, which is not a filename", name, got)
		}
	}
}

// A name that is already fine should survive, so the report and the graph say
// what the person uploaded.
func TestUploadKeepsAReasonableName(t *testing.T) {
	for _, name := range []string{"team.csv", "sales-2026.tsv", "dump.sql", "a_b.CSV"} {
		if got := safeUploadName(name); got != name {
			t.Errorf("%q was rewritten to %q for no reason", name, got)
		}
	}
}

// An upload with nothing attached is a mistake worth a clear answer rather than
// a panic or an empty import.
func TestUploadWithNoFileIsRefused(t *testing.T) {
	app := &App{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, uploadPath, strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=nope")
	app.handleUpload(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("an empty upload was accepted: %d %s", rec.Code, rec.Body.String())
	}
}

// Only POST. A GET to an upload endpoint is a mistake, not a download.
func TestUploadRejectsOtherMethods(t *testing.T) {
	app := &App{}
	rec := httptest.NewRecorder()
	app.handleUpload(rec, httptest.NewRequest(http.MethodGet, uploadPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET returned %d, want 405", rec.Code)
	}
}
