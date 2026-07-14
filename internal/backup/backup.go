package backup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Manifest describes a backup written under ~/.cursor-rebind/backups/<id>/.
type Manifest struct {
	ID        string            `json:"id"`
	CreatedAt time.Time         `json:"createdAt"`
	Files     map[string]string `json:"files"` // logical name → relative path in backup
	Note      string            `json:"note,omitempty"`
}

// Dir returns the backups root (~/.cursor-rebind/backups).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cursor-rebind", "backups"), nil
}

// Create makes a new backup directory and returns its absolute path + id.
func Create(note string) (id, absDir string, man *Manifest, err error) {
	root, err := Dir()
	if err != nil {
		return "", "", nil, err
	}
	id = time.Now().UTC().Format("20060102T150405Z")
	absDir = filepath.Join(root, id)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", "", nil, err
	}
	man = &Manifest{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		Files:     map[string]string{},
		Note:      note,
	}
	return id, absDir, man, nil
}

// CopyFile copies src into the backup dir as name and records it in the manifest.
func CopyFile(backupDir string, man *Manifest, logical, src string) error {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dstName := filepath.Base(src)
	if logical != "" {
		dstName = logical + filepath.Ext(src)
		if filepath.Ext(src) == "" {
			dstName = logical
		}
	}
	// Keep unique names for sidecars.
	dstName = filepath.Base(src)
	dst := filepath.Join(backupDir, dstName)
	if err := copyFile(src, dst); err != nil {
		return err
	}
	man.Files[src] = dstName
	return nil
}

// CopyTree copies a directory tree into backupDir/relName.
func CopyTree(backupDir string, man *Manifest, logical, src string) error {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dst := filepath.Join(backupDir, logical)
	if err := copyDir(src, dst); err != nil {
		return err
	}
	man.Files[src] = logical
	return nil
}

// WriteManifest persists manifest.json.
func WriteManifest(backupDir string, man *Manifest) error {
	raw, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(backupDir, "manifest.json"), raw, 0o644)
}

// LoadManifest reads a backup's manifest.
func LoadManifest(backupDir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(backupDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var man Manifest
	if err := json.Unmarshal(raw, &man); err != nil {
		return nil, err
	}
	return &man, nil
}

// List returns backup ids newest first.
func List() ([]Manifest, error) {
	root, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Manifest
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !e.IsDir() {
			continue
		}
		man, err := LoadManifest(filepath.Join(root, e.Name()))
		if err != nil {
			out = append(out, Manifest{ID: e.Name(), Note: "(missing manifest)"})
			continue
		}
		out = append(out, *man)
	}
	return out, nil
}

// RestoreFile copies a backed-up file back to its original path.
func RestoreFile(backupDir string, man *Manifest, originalPath string) error {
	rel, ok := man.Files[originalPath]
	if !ok {
		return fmt.Errorf("no backup entry for %s", originalPath)
	}
	src := filepath.Join(backupDir, rel)
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if st.IsDir() {
		// Replace destination directory.
		_ = os.RemoveAll(originalPath)
		return copyDir(src, originalPath)
	}
	if err := os.MkdirAll(filepath.Dir(originalPath), 0o755); err != nil {
		return err
	}
	return copyFile(src, originalPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}
