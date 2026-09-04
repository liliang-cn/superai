package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/superai-desktop/backend"
)

// A one-pixel PNG, so the sniffer has real bytes to work with.
var onePixelPNG = mustDecode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func mustDecode(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// pasteApp returns an App whose workspace is a temp dir.
func pasteApp(t *testing.T) *App {
	t.Helper()
	a := &App{settings: &backend.Settings{WorkspaceDir: t.TempDir()}}
	return a
}

// The point of the whole feature: a screenshot on the clipboard becomes an
// attachment. It arrives as a data URL because that is what FileReader makes.
func TestAPastedScreenshotLandsInUploads(t *testing.T) {
	a := pasteApp(t)
	rel, err := a.ImportPastedFile("image.png", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(onePixelPNG))
	if err != nil {
		t.Fatalf("paste: %v", err)
	}
	if !strings.HasPrefix(rel, uploadsSubdir+"/") {
		t.Fatalf("landed outside uploads: %q", rel)
	}
	// It has to end in an image extension or useAttachments will send it to
	// the model as a document reference instead of as a picture.
	if !strings.HasSuffix(rel, ".png") {
		t.Fatalf("a PNG was not named one: %q", rel)
	}
	got, err := os.ReadFile(filepath.Join(a.workspaceDir(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(onePixelPNG) {
		t.Fatalf("the file on disk is not the bytes that were pasted")
	}
	// Every browser calls a pasted screenshot "image.png"; keeping that name
	// would number a morning's worth of them "image (1)", "image (2)".
	if strings.HasPrefix(filepath.Base(rel), "image") {
		t.Errorf("the generic clipboard name survived: %q", rel)
	}
}

// The name comes from the page and may be anything at all.
func TestAPastedNameCannotEscapeTheWorkspace(t *testing.T) {
	a := pasteApp(t)
	for _, name := range []string{"../../../etc/passwd", "/etc/passwd", `..\..\win.ini`, "", "..", "a/b/c.png"} {
		rel, err := a.ImportPastedFile(name, base64.StdEncoding.EncodeToString(onePixelPNG))
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if !strings.HasPrefix(rel, uploadsSubdir+"/") || strings.Contains(strings.TrimPrefix(rel, uploadsSubdir+"/"), "/") {
			t.Errorf("%q became %q, which is not a file directly in uploads", name, rel)
		}
	}
}

// The bytes decide the extension, not the name: a file called .txt that is
// really a PNG would otherwise be described to the model as a document, and
// one called .png that is really text would be sent to the vision model.
func TestThePastedExtensionComesFromTheBytes(t *testing.T) {
	a := pasteApp(t)
	rel, err := a.ImportPastedFile("notes.txt", base64.StdEncoding.EncodeToString(onePixelPNG))
	if err != nil {
		t.Fatalf("paste: %v", err)
	}
	if filepath.Ext(rel) != ".png" {
		t.Errorf("PNG bytes named %q kept the wrong extension", rel)
	}
	rel, err = a.ImportPastedFile("shot.png", base64.StdEncoding.EncodeToString([]byte("just some text, no header at all")))
	if err != nil {
		t.Fatalf("paste: %v", err)
	}
	if filepath.Ext(rel) == ".png" {
		t.Errorf("text bytes were left named %q and would go to the vision model", rel)
	}
}

// Two pastes in the same second get the same generated name. They must not
// get the same file.
func TestTwoPastesDoNotOverwriteEachOther(t *testing.T) {
	a := pasteApp(t)
	first, err := a.ImportPastedFile("image.png", base64.StdEncoding.EncodeToString(onePixelPNG))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := a.ImportPastedFile("image.png", base64.StdEncoding.EncodeToString(onePixelPNG))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first == second {
		t.Fatalf("both pastes claim %q", first)
	}
}

func TestAPasteThatIsNotAFileIsRefused(t *testing.T) {
	a := pasteApp(t)
	if _, err := a.ImportPastedFile("x.png", "not base64 at all !!!"); err == nil {
		t.Error("garbage was accepted as a file")
	}
	if _, err := a.ImportPastedFile("x.png", ""); err == nil {
		t.Error("an empty paste was accepted as a file")
	}
	big := base64.StdEncoding.EncodeToString(make([]byte, maxPastedBytes+1))
	if _, err := a.ImportPastedFile("x.bin", big); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("an oversized paste was not refused with a size reason: %v", err)
	}
}
