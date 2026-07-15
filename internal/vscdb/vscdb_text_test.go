package vscdb_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/vscdb"
	_ "modernc.org/sqlite"
)

func TestSetItemRawWritesTextAffinity(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.vscdb")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT UNIQUE, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db, err = vscdb.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := vscdb.SetItemRaw(db, "workbench.parts.embeddedAuxBarEditor.state", []byte(`{"activeGroup":1}`)); err != nil {
		t.Fatal(err)
	}
	var typ string
	if err := db.QueryRow(`SELECT typeof(value) FROM ItemTable WHERE key = ?`, "workbench.parts.embeddedAuxBarEditor.state").Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if typ != "text" {
		t.Fatalf("typeof=%q want text", typ)
	}
}

func TestNormalizeItemTableText(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.vscdb")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT UNIQUE, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	// Force blob affinity by binding []byte.
	if _, err := db.Exec(`INSERT INTO ItemTable(key, value) VALUES(?, ?)`, "k", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	var typ string
	_ = db.QueryRow(`SELECT typeof(value) FROM ItemTable WHERE key='k'`).Scan(&typ)
	if typ != "blob" {
		t.Fatalf("setup typeof=%q", typ)
	}
	_ = db.Close()

	db, err = vscdb.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	n, err := vscdb.NormalizeItemTableText(db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("normalized=%d", n)
	}
	_ = db.QueryRow(`SELECT typeof(value) FROM ItemTable WHERE key='k'`).Scan(&typ)
	if typ != "text" {
		t.Fatalf("typeof=%q want text", typ)
	}
}
