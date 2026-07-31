package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
