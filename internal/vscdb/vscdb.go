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

// GetItemJSON loads a JSON value from ItemTable by key.
func GetItemJSON(db *sql.DB, key string, dest any) (bool, error) {
	var raw []byte
	err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
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
