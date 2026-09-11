package vscdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// HeadersSource says where LoadComposerHeaders found the chat index.
type HeadersSource int

const (
	HeadersNone HeadersSource = iota
	HeadersItemTable
	HeadersSQLTable
)

func (s HeadersSource) String() string {
	switch s {
	case HeadersItemTable:
		return "itemTable"
	case HeadersSQLTable:
		return "sqlTable"
	default:
		return "none"
	}
}

// HasComposerHeadersTable reports whether the dedicated composerHeaders table exists.
func HasComposerHeadersTable(db *sql.DB) bool {
	if db == nil {
		return false
	}
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='composerHeaders'`).Scan(&name)
	return err == nil && name == "composerHeaders"
}

// LoadComposerHeaders loads the global chat index.
// Cursor historically stored it as ItemTable["composer.composerHeaders"].
// Newer builds (observed on 3.19+) migrate to a dedicated composerHeaders SQL
// table and may omit the ItemTable blob entirely (migratedToTable / tableGateEnabled).
func LoadComposerHeaders(db *sql.DB) (ComposerHeaders, HeadersSource, error) {
	var headers ComposerHeaders
	if db == nil {
		return headers, HeadersNone, fmt.Errorf("nil db")
	}
	ok, err := GetItemJSON(db, "composer.composerHeaders", &headers)
	if err != nil {
		return headers, HeadersNone, err
	}
	if ok {
		return headers, HeadersItemTable, nil
	}
	if !HasComposerHeadersTable(db) {
		return headers, HeadersNone, nil
	}
	list, err := loadComposerHeadersFromSQLTable(db)
	if err != nil {
		return headers, HeadersNone, err
	}
	headers.AllComposers = list
	if len(list) == 0 {
		// Table exists but empty — still a valid Cursor 3.x store.
		return headers, HeadersSQLTable, nil
	}
	return headers, HeadersSQLTable, nil
}

func loadComposerHeadersFromSQLTable(db *sql.DB) ([]ComposerMeta, error) {
	rows, err := db.Query(`SELECT composerId, workspaceId, value FROM composerHeaders`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ComposerMeta
	for rows.Next() {
		var id, wid, val string
		if err := rows.Scan(&id, &wid, &val); err != nil {
			return nil, err
		}
		meta := ComposerMeta{ComposerID: id}
		if val != "" {
			if err := json.Unmarshal([]byte(val), &meta); err != nil {
				// Keep a minimal stub so IDs are not dropped.
				meta = ComposerMeta{ComposerID: id}
			}
		}
		if meta.ComposerID == "" {
			meta.ComposerID = id
		}
		if meta.WorkspaceIdentifier == nil && wid != "" {
			meta.WorkspaceIdentifier = &WorkspaceIdentifier{ID: wid}
		} else if meta.WorkspaceIdentifier != nil && meta.WorkspaceIdentifier.ID == "" && wid != "" {
			meta.WorkspaceIdentifier.ID = wid
		}
		out = append(out, meta)
	}
	return out, rows.Err()
}

// PersistComposerHeadersItemTable writes the ItemTable blob when the store still
// uses it (or dual-wrote before migration). Skip when headers only live in SQL
// so we do not resurrect a stale ItemTable index Cursor already abandoned.
func PersistComposerHeadersItemTable(db *sql.DB, headers ComposerHeaders, source HeadersSource) error {
	if source != HeadersItemTable {
		return nil
	}
	return SetItemJSON(db, "composer.composerHeaders", headers)
}
