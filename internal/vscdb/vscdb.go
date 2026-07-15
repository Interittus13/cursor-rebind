package vscdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// OpenReadOnly opens a Cursor state.vscdb for reading.
// Prefer a snapshot when WAL may be active; callers can pass the live path for ItemTable-only reads.
func OpenReadOnly(dbPath string) (*sql.DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("stat %s: %w", dbPath, err)
	}
	// immutable=1 avoids creating -wal/-shm next to a live DB.
	dsn := fmt.Sprintf("file:%s?mode=ro&immutable=1", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		// Fallback without immutable (some environments dislike it with missing wal).
		dsn = fmt.Sprintf("file:%s?mode=ro", dbPath)
		db, err = sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

// OpenReadWrite opens a Cursor state.vscdb for mutation. Caller must ensure Cursor is closed.
func OpenReadWrite(dbPath string) (*sql.DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("stat %s: %w", dbPath, err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// CheckpointWAL flushes the WAL into the main DB file so a later Cursor
// reopen cannot miss our writes when only the -wal sidecar was updated.
func CheckpointWAL(db *sql.DB) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// SetItemJSON writes a JSON value into ItemTable (insert or replace).
func SetItemJSON(db *sql.DB, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return SetItemRaw(db, key, raw)
}

// SetItemRaw writes into ItemTable as SQLITE TEXT, not BLOB.
// Cursor's storage layer JSON.parses ItemTable values; if they are stored as
// BLOBs, Electron hands the renderer a Uint8Array and JSON.parse coerces it to
// "123,34,…" (byte decimals) — which fails with "position 3".
func SetItemRaw(db *sql.DB, key string, raw []byte) error {
	_, err := db.Exec(`INSERT INTO ItemTable(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(raw))
	return err
}

// NormalizeItemTableText rewrites any ItemTable BLOB values to TEXT affinity.
// Safe for Cursor state DBs: ItemTable is always textual (JSON or plain strings).
func NormalizeItemTableText(db *sql.DB) (int, error) {
	res, err := db.Exec(`UPDATE ItemTable SET value = CAST(value AS TEXT) WHERE typeof(value) = 'blob'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteItem removes a key from ItemTable.
func DeleteItem(db *sql.DB, key string) error {
	_, err := db.Exec(`DELETE FROM ItemTable WHERE key = ?`, key)
	return err
}

// ListItemKeysLike returns ItemTable keys matching a SQLite LIKE pattern.
func ListItemKeysLike(db *sql.DB, like string) ([]string, error) {
	rows, err := db.Query(`SELECT key FROM ItemTable WHERE key LIKE ?`, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return out, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetItemRaw returns the raw blob for a key.
func GetItemRaw(db *sql.DB, key string) ([]byte, bool, error) {
	var raw []byte
	err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// GetDiskKVRaw returns a value from cursorDiskKV.
func GetDiskKVRaw(db *sql.DB, key string) ([]byte, bool, error) {
	var raw []byte
	err := db.QueryRow(`SELECT value FROM cursorDiskKV WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// GetItemJSON loads a JSON value from ItemTable by key.
func GetItemJSON(db *sql.DB, key string, dest any) (bool, error) {
	raw, ok, err := GetItemRaw(db, key)
	if err != nil || !ok {
		return ok, err
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, fmt.Errorf("decode %s: %w", key, err)
	}
	return true, nil
}

// ComposerData is the per-workspace sidebar / tab state.
type ComposerData struct {
	AllComposers                 []ComposerMeta `json:"allComposers"`
	SelectedComposerIDs          []string       `json:"selectedComposerIds"`
	LastFocusedComposerIDs       []string       `json:"lastFocusedComposerIds"`
	HasMigratedComposerData      bool           `json:"hasMigratedComposerData"`
	HasMigratedMultipleComposers bool           `json:"hasMigratedMultipleComposers"`
}

// ComposerMeta is a chat header entry (workspace or global).
type ComposerMeta struct {
	Type                string               `json:"type"`
	ComposerID          string               `json:"composerId"`
	Name                string               `json:"name"`
	CreatedAt           int64                `json:"createdAt"`
	LastUpdatedAt       int64                `json:"lastUpdatedAt"`
	UnifiedMode         string               `json:"unifiedMode"`
	Subtitle            string               `json:"subtitle"`
	WorkspaceIdentifier *WorkspaceIdentifier `json:"workspaceIdentifier,omitempty"`
}

// WorkspaceIdentifier links a chat to a workspace in Cursor 3.0+.
type WorkspaceIdentifier struct {
	ID  string       `json:"id"`
	URI *WorkspaceURI `json:"uri"`
}

// WorkspaceURI is the VS Code-style URI object stored in headers.
type WorkspaceURI struct {
	FsPath   string `json:"fsPath"`
	External string `json:"external"`
	Path     string `json:"path"`
	Scheme   string `json:"scheme"`
}

// ComposerHeaders is the Cursor 3.0+ global chat index.
type ComposerHeaders struct {
	AllComposers []ComposerMeta `json:"allComposers"`
}

// SchemaVersion describes how chats are indexed for a workspace.
type SchemaVersion string

const (
	SchemaPre3    SchemaVersion = "pre-3.0"    // allComposers in workspace DB
	SchemaCursor3 SchemaVersion = "3.0+"       // migrated; global composerHeaders
	SchemaUnknown SchemaVersion = "unknown"
)

// DetectWorkspaceSchema classifies a workspace composer.composerData blob.
func DetectWorkspaceSchema(data *ComposerData) SchemaVersion {
	if data == nil {
		return SchemaUnknown
	}
	if data.HasMigratedComposerData || (len(data.AllComposers) == 0 && (len(data.SelectedComposerIDs) > 0 || data.HasMigratedMultipleComposers)) {
		return SchemaCursor3
	}
	if len(data.AllComposers) > 0 {
		return SchemaPre3
	}
	if data.HasMigratedMultipleComposers {
		return SchemaCursor3
	}
	return SchemaUnknown
}
