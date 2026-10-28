package app

// Publishing the Android release: `superleo android publish` copies an APK
// into <data dir>/android, hashes it and points latest.json at it. A running
// core serves the new files at once (android_update.go); nothing restarts.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AndroidRelease is latest.json.
type AndroidRelease struct {
	VersionCode int64  `json:"versionCode"`
	VersionName string `json:"versionName"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Notes       string `json:"notes,omitempty"`
}

// androidKeep is how many APKs stay in the directory: the new one and the
// two before it, for a phone halfway through downloading an older one.
const androidKeep = 3

// publishAndroid copies the APK at src into dir as superleo-<name>.apk, hashes
// it, and only then points latest.json at it, so a phone never reads a
// manifest naming a file that is not all there.
func publishAndroid(dir, src string, code int64, name, notes string) (AndroidRelease, error) {
	if code <= 0 {
		return AndroidRelease{}, fmt.Errorf("versionCode must be positive")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strconv.FormatInt(code, 10)
	}
	file := "superleo-" + name + ".apk"
	if !androidFile.MatchString(file) {
		return AndroidRelease{}, fmt.Errorf("versionName %q does not make a plain file name", name)
	}
	in, err := os.Open(src)
	if err != nil {
		return AndroidRelease{}, err
	}
	defer in.Close()
	var magic [4]byte
	if _, err := io.ReadFull(in, magic[:]); err != nil || string(magic[:]) != "PK\x03\x04" {
		return AndroidRelease{}, fmt.Errorf("%s is not an APK (no zip header)", src)
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return AndroidRelease{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return AndroidRelease{}, err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return AndroidRelease{}, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), in); err != nil {
		tmp.Close()
		return AndroidRelease{}, err
	}
	if err := tmp.Close(); err != nil {
		return AndroidRelease{}, err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	if err := os.Rename(tmp.Name(), filepath.Join(dir, file)); err != nil {
		return AndroidRelease{}, err
	}
	rel := AndroidRelease{VersionCode: code, VersionName: name, URL: file, SHA256: hex.EncodeToString(h.Sum(nil)), Notes: strings.TrimSpace(notes)}
	b, _ := json.MarshalIndent(rel, "", "  ")
	if err := replaceFile(filepath.Join(dir, androidManifest), append(b, '\n')); err != nil {
		return AndroidRelease{}, err
	}
	pruneAndroid(dir, file)
	return rel, nil
}

// pruneAndroid removes all but the newest androidKeep APKs; keep stays.
func pruneAndroid(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type apk struct {
		name string
		mod  time.Time
	}
	var apks []apk
	for _, e := range entries {
		if e.Type().IsRegular() && androidFile.MatchString(e.Name()) && e.Name() != keep {
			if info, err := e.Info(); err == nil {
				apks = append(apks, apk{e.Name(), info.ModTime()})
			}
		}
	}
	sort.Slice(apks, func(i, j int) bool { return apks[i].mod.After(apks[j].mod) })
	for i, a := range apks {
		if i >= androidKeep-1 {
			_ = os.Remove(filepath.Join(dir, a.name))
		}
	}
}

// replaceFile writes data beside path and renames it over, so a reader sees
// the old file or the new one, never half of one.
func replaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), path)
}

// AndroidMain is `superleo android`:
//
//	superleo android publish --code 3 --name 0.3.0 [--notes "…"] app-release.apk
//	superleo android show
//
// It writes into <data dir>/android (SUPERLEO_HOME). On a core in a pod, copy
// the APK in and run it there, with the pod's SUPERLEO_HOME.
func AndroidMain(argv []string) {
	usage := func() {
		fmt.Fprintln(os.Stderr, "usage: superleo android publish --code <versionCode> [--name <versionName>] [--notes <text>] <file.apk>")
		fmt.Fprintln(os.Stderr, "       superleo android show")
		fmt.Fprintln(os.Stderr, "  the release is kept in", androidReleaseDir(), "(set SUPERLEO_HOME to change)")
		os.Exit(2)
	}
	if len(argv) == 0 {
		usage()
	}
	switch argv[0] {
	case "publish":
		fl := flag.NewFlagSet("android publish", flag.ExitOnError)
		fl.Usage = usage
		code := fl.Int64("code", 0, "the APK's versionCode; phones are offered it when it is higher than theirs")
		name := fl.String("name", "", "the APK's versionName, e.g. 0.3.0")
		notes := fl.String("notes", "", "what changed, shown beside the install button")
		_ = fl.Parse(argv[1:])
		if fl.NArg() != 1 || *code <= 0 {
			usage()
		}
		rel, err := publishAndroid(androidReleaseDir(), fl.Arg(0), *code, *name, *notes)
		if err != nil {
			fmt.Fprintln(os.Stderr, "superleo:", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "published %s (versionCode %d) to %s\n", rel.VersionName, rel.VersionCode, androidReleaseDir())
		fmt.Println(rel.SHA256)
	case "show":
		b, err := os.ReadFile(filepath.Join(androidReleaseDir(), androidManifest))
		if err != nil {
			fmt.Fprintln(os.Stderr, "superleo: no release published:", err)
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(b)
	default:
		usage()
	}
}
