package vscdb_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Interittus13/cursor-rebind/internal/vscdb"
	_ "modernc.org/sqlite"
)

func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE composerHeaders (
			composerId TEXT PRIMARY KEY,
			workspaceId TEXT,
			createdAt INTEGER,
			lastUpdatedAt INTEGER,
			isArchived INTEGER,
			isSubagent INTEGER,
			recency INTEGER,
			checkpointAt INTEGER,
			value TEXT
		)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestLoadComposerHeadersFromItemTable(t *testing.T) {
	db := openTempDB(t)
	blob := vscdb.ComposerHeaders{AllComposers: []vscdb.ComposerMeta{{
		ComposerID: "a",
		Name:       "Hello",
		WorkspaceIdentifier: &vscdb.WorkspaceIdentifier{
			ID: "ws1",
			URI: &vscdb.WorkspaceURI{FsPath: "/tmp/p", Path: "/tmp/p", Scheme: "file"},
		},
	}}}
	raw, _ := json.Marshal(blob)
	if _, err := db.Exec(`INSERT INTO ItemTable(key, value) VALUES(?, ?)`, "composer.composerHeaders", string(raw)); err != nil {
		t.Fatal(err)
	}
	got, src, err := vscdb.LoadComposerHeaders(db)
	if err != nil {
		t.Fatal(err)
	}
	if src != vscdb.HeadersItemTable {
		t.Fatalf("src=%v", src)
	}
	if len(got.AllComposers) != 1 || got.AllComposers[0].Name != "Hello" {
		t.Fatalf("%+v", got)
	}
}

func TestLoadComposerHeadersFromSQLTableWhenItemMissing(t *testing.T) {
	db := openTempDB(t)
	val := `{"composerId":"b","name":"FromSQL","workspaceIdentifier":{"id":"shell","uri":{"fsPath":"/home/u/proj","path":"/home/u/proj","scheme":"file"}}}`
	if _, err := db.Exec(`INSERT INTO composerHeaders(composerId, workspaceId, createdAt, lastUpdatedAt, isArchived, isSubagent, recency, checkpointAt, value)
		VALUES(?,?,1,1,0,0,1,1,?)`, "b", "shell", val); err != nil {
		t.Fatal(err)
	}
	got, src, err := vscdb.LoadComposerHeaders(db)
	if err != nil {
		t.Fatal(err)
	}
	if src != vscdb.HeadersSQLTable {
		t.Fatalf("src=%v want sqlTable", src)
	}
	if len(got.AllComposers) != 1 {
		t.Fatalf("len=%d", len(got.AllComposers))
	}
	c := got.AllComposers[0]
	if c.Name != "FromSQL" || c.WorkspaceIdentifier == nil || c.WorkspaceIdentifier.ID != "shell" {
		t.Fatalf("%+v", c)
	}
	if c.WorkspaceIdentifier.URI == nil || c.WorkspaceIdentifier.URI.FsPath != "/home/u/proj" {
		t.Fatalf("uri=%+v", c.WorkspaceIdentifier.URI)
	}
}

func TestLoadComposerHeadersNeither(t *testing.T) {
	db := openTempDB(t)
	// Drop SQL table to simulate ancient DB
	if _, err := db.Exec(`DROP TABLE composerHeaders`); err != nil {
		t.Fatal(err)
	}
	got, src, err := vscdb.LoadComposerHeaders(db)
	if err != nil {
		t.Fatal(err)
	}
	if src != vscdb.HeadersNone || len(got.AllComposers) != 0 {
		t.Fatalf("src=%v n=%d", src, len(got.AllComposers))
	}
}

func TestPersistComposerHeadersItemTableSkippedForSQLSource(t *testing.T) {
	db := openTempDB(t)
	headers := vscdb.ComposerHeaders{AllComposers: []vscdb.ComposerMeta{{ComposerID: "x", Name: "N"}}}
	if err := vscdb.PersistComposerHeadersItemTable(db, headers, vscdb.HeadersSQLTable); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM ItemTable WHERE key='composer.composerHeaders'`).Scan(&n)
	if n != 0 {
		t.Fatalf("unexpected ItemTable write n=%d", n)
	}
}
