package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/liliang-cn/superai-desktop/backend"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// uploadsSubdir is where dragged/attached files land inside the agent workspace.
// Defined in backend so the deliverables list can exclude the same directory.
const uploadsSubdir = backend.UploadsSubdir

// workspaceDir returns the configured agent workspace root (the sandbox root).
func (a *App) workspaceDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settings != nil {
		return strings.TrimSpace(a.settings.WorkspaceDir)
	}
	return ""
}

// ImportFiles copies host files into <workspace>/uploads and returns their
// workspace-relative paths (e.g. "uploads/report.xlsx"), which the agent can
// read with the read_document tool. Name collisions are de-duplicated.
func (a *App) ImportFiles(paths []string) ([]string, error) {
	ws := a.workspaceDir()
	if ws == "" {
		return nil, fmt.Errorf("workspace not configured")
	}
	dstDir := filepath.Join(ws, uploadsSubdir)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, fmt.Errorf("create uploads dir: %w", err)
	}

	var rels []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return rels, fmt.Errorf("stat %s: %w", filepath.Base(p), err)
		}
		if info.IsDir() {
			continue // skip dropped directories
		}
		target := uniquePath(filepath.Join(dstDir, filepath.Base(p)))
		if err := copyFile(p, target); err != nil {
			return rels, fmt.Errorf("import %s: %w", filepath.Base(p), err)
		}
		if rel, rerr := filepath.Rel(ws, target); rerr == nil {
			rels = append(rels, filepath.ToSlash(rel))
		} else {
			rels = append(rels, target)
		}
	}
	// Registered as the user's, so that a file the agent later writes into the
	// same directory is still recognised as something it produced.
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc != nil {
		svc.NoteImported(rels)
	}
	return rels, nil
}

// maxPastedBytes caps one pasted file.
//
// A paste travels as base64 inside the JSON-RPC argument, and the serve-mode
// dispatcher reads at most 32MB of a request body. Base64 costs a third on
// top, so the decoded ceiling has to sit well under that — otherwise an
// oversized paste arrives as a truncated body and fails as a parse error
// instead of as a size one, which is a much worse thing to read.
const maxPastedBytes = 16 << 20

// ImportPastedFile writes one clipboard file into <workspace>/uploads and
// returns its workspace-relative path, the same shape ImportFiles returns.
//
// A drop and a file picker both hand over a path on this machine. A paste
// never does: the clipboard holds bytes, and in serve mode those bytes are on
// a laptop the server cannot see. So they come in-band, base64 in the
// argument, and this is the one import that writes a file it was handed
// rather than copying one it was pointed at.
func (a *App) ImportPastedFile(name string, data string) (string, error) {
	// A browser's FileReader produces a data URL ("data:image/png;base64,…").
	// Take it whole rather than making every caller strip the prefix.
	if strings.HasPrefix(data, "data:") {
		if i := strings.Index(data, ","); i >= 0 {
			data = data[i+1:]
		}
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
	if err != nil {
		return "", fmt.Errorf("pasted data is not base64: %w", err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("the pasted file is empty")
	}
	if len(raw) > maxPastedBytes {
		return "", fmt.Errorf("the pasted file is %dMB; the limit is %dMB", len(raw)>>20, maxPastedBytes>>20)
	}

	ws := a.workspaceDir()
	if ws == "" {
		return "", fmt.Errorf("workspace not configured")
	}
	dstDir := filepath.Join(ws, uploadsSubdir)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", fmt.Errorf("create uploads dir: %w", err)
	}
	target, err := writeNew(filepath.Join(dstDir, pastedName(name, raw)), raw)
	if err != nil {
		return "", fmt.Errorf("write the pasted file: %w", err)
	}

	rel := target
	if r, rerr := filepath.Rel(ws, target); rerr == nil {
		rel = filepath.ToSlash(r)
	}
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc != nil {
		svc.NoteImported([]string{rel})
	}
	return rel, nil
}

// pastedName picks the filename a pasted file lands under.
//
// Two things it has to get right. The extension is decided by the bytes and
// not by the name, because the attachment list sends anything with an image
// extension to the vision model and a mislabelled file would arrive there as
// noise. And a screenshot has no name worth keeping — every browser calls it
// "image.png", so a morning of pasting would read "image (1)", "image (2)" —
// which a timestamp replaces with something that says when it came from.
func pastedName(name string, data []byte) string {
	base := safeUploadName(name)
	switch strings.ToLower(base) {
	case "upload.dat", "image.png", "image.jpeg", "image.jpg", "image", "clipboard.png", "pasted.png":
		base = "pasted-" + time.Now().Format("20060102-150405")
	}
	ext := filepath.Ext(base)
	if sniffed := sniffExt(data); sniffed != "" {
		return strings.TrimSuffix(base, ext) + sniffed
	}
	// The bytes are of a kind we do not recognise, so the name may keep
	// speaking for them — except when what it claims is a picture, which is
	// the one claim the attachment list acts on.
	if ext == "" || imageExtRE.MatchString(ext) {
		return strings.TrimSuffix(base, ext) + ".bin"
	}
	return base
}

// imageExtRE is the frontend's IMAGE_RE in useAttachments.ts: the extensions
// that decide a file is sent to the model as a picture rather than named to it
// as a document. Keep the two in step.
var imageExtRE = regexp.MustCompile(`(?i)^\.(png|jpe?g|gif|webp|bmp|tiff?)$`)

// sniffExt returns the extension the content itself asks for, or "" when the
// bytes are of a kind the name may keep speaking for.
func sniffExt(data []byte) string {
	ct, _, _ := strings.Cut(http.DetectContentType(data), ";")
	switch ct {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "application/pdf":
		return ".pdf"
	}
	return ""
}

// PickFiles opens a native file picker and imports the chosen files into the
// workspace, returning their workspace-relative paths.
func (a *App) PickFiles() ([]string, error) {
	sel, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Attach files",
		Filters: []runtime.FileFilter{{
			DisplayName: "Documents & Images",
			Pattern:     "*.docx;*.xlsx;*.pptx;*.pdf;*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp;*.tiff;*.txt;*.md;*.csv;*.json",
		}},
	})
	if err != nil {
		return nil, err
	}
	if len(sel) == 0 {
		return nil, nil
	}
	return a.ImportFiles(sel)
}

// writeNew writes data to p, or to the first free " (n)" variant of it, and
// returns where it landed.
//
// uniquePath then Create would do the same in two steps, and two pastes made
// in the same second — which is what Promise.all does with a multi-file paste
// — would agree on a free name and one would land on top of the other. The
// exclusive create is what makes the name a claim rather than an observation.
func writeNew(p string, data []byte) (string, error) {
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	for i := 0; ; i++ {
		cand := p
		if i > 0 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		f, err := os.OpenFile(cand, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			// A half-written image would reach the vision model as a broken
			// file rather than as a failure to attach one.
			_ = os.Remove(cand)
			if werr != nil {
				return "", werr
			}
			return "", cerr
		}
		return cand, nil
	}
}

// uniquePath returns p if free, else inserts " (n)" before the extension.
func uniquePath(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// ExportWorkspaceFile copies a deliverable out of the workspace to wherever the
// user chooses, through the native save dialog.
//
// The preview modal could open a file with its owning app but had no way to keep
// it: a produced report lived in the workspace and the only route out was to go
// and find it in Finder. Returns "ok", "cancelled", or the failure.
func (a *App) ExportWorkspaceFile(path string) string {
	ws := a.workspaceDir()
	if ws == "" {
		return "workspace not configured"
	}
	src := filepath.Join(ws, filepath.FromSlash(path))
	if _, err := os.Stat(src); err != nil {
		return fmt.Sprintf("no such file: %s", filepath.Base(path))
	}
	dst, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save file",
		DefaultFilename: filepath.Base(path),
	})
	if err != nil {
		return err.Error()
	}
	// An empty path is the user dismissing the dialog, which is not a failure.
	if strings.TrimSpace(dst) == "" {
		return "cancelled"
	}
	if err := copyFile(src, dst); err != nil {
		return fmt.Sprintf("save failed: %v", err)
	}
	return "ok"
}
