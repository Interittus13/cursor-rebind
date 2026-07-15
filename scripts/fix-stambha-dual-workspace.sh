#!/usr/bin/env bash
# DEPRECATED one-off: prefer the built-in consolidate failsafe:
#   cursor-rebind repair --to /home/ulap92/Documents/Arpit/_Others/GitHub/Stambha --yes
#
# Kept as a reference for the Stambha dual-workspace incident. Direction must be:
# move named chats onto the empty shell Cursor opens (KEEP=57022…), then remove
# the data leftover (MOVE_FROM=06e84…). Never delete the shell and keep the leftover.
#
# Fix Stambha dual workspaceStorage IDs so IDE agent history matches Agents Window.
#
# Root cause: two workspace ids point at the same folder:
#   57022b89…  ← shell Cursor actually opens for this path (Agents + IDE bind here)
#   06e84e9d…  ← data leftover that holds the named chats after migrate/path rewrite
#
# Earlier consolidate moved chats onto 06e84… and deleted 57022…. Cursor then
# reminted 57022… on reopen — empty again, so history stayed blank.
#
# Correct direction: move chats onto 57022… (the live shell), then orphan/remove
# 06e84… and any other Stambha duplicate ids.
#
# Usage (IMPORTANT — do not run from inside a Cursor agent that will die on quit):
#   1. Fully quit Cursor (File → Exit / all windows)
#   2. In a normal terminal:
#        bash scripts/fix-stambha-dual-workspace.sh
#   3. Reopen only: ~/Documents/Arpit/_Others/GitHub/Stambha
#
set -euo pipefail

# KEEP = shell Cursor opens; MOVE_FROM = data-holding leftover(s)
KEEP="${STAMBHA_KEEP_WSID:-57022b89b0c2d52238e6a9507ec5fb1b}"
MOVE_FROM="${STAMBHA_MOVE_FROM_WSID:-06e84e9db255de8fdfcf3f2dc1425c5e}"
# Extra ghost metadata / storage ids for the same folder
ALSO_DROP="${STAMBHA_ALSO_DROP:-9df31d7b32beb1a016e3e76811b4c933}"
TO="${STAMBHA_TO:-/home/ulap92/Documents/Arpit/_Others/GitHub/Stambha}"
WS="${HOME}/.config/Cursor/User/workspaceStorage"
GDB="${HOME}/.config/Cursor/User/globalStorage/state.vscdb"

if pgrep -f '/usr/share/cursor/cursor' >/dev/null 2>&1 || pgrep -x Cursor >/dev/null 2>&1; then
  echo "error: Cursor is still running."
  echo "Quit Cursor completely first, then re-run this script from a normal terminal."
  echo "(Do not run a kill-Cursor command from inside a Cursor chat — it closes this session.)"
  exit 1
fi

if [[ ! -f "${GDB}" ]]; then
  echo "error: missing ${GDB}"
  exit 1
fi

export KEEP MOVE_FROM ALSO_DROP TO WS GDB
python3 <<'PY'
import json, os, shutil, sqlite3
from pathlib import Path

keep = os.environ["KEEP"]
move_from = os.environ["MOVE_FROM"]
also_drop = [x for x in os.environ.get("ALSO_DROP", "").split() if x]
to = os.environ["TO"]
ws = Path(os.environ["WS"])
gdb = Path(os.environ["GDB"])

bak = Path.home() / ".cursor-rebind" / "backups" / "manual-stambha-consolidate-v2"
bak.mkdir(parents=True, exist_ok=True)
shutil.copy2(gdb, bak / "state.vscdb")
for side in (gdb.parent / "state.vscdb-wal", gdb.parent / "state.vscdb-shm"):
    if side.exists():
        shutil.copy2(side, bak / side.name)
for wid in [move_from, keep, *also_drop]:
    src = ws / wid
    if src.exists():
        dest = bak / wid
        if dest.exists():
            shutil.rmtree(dest)
        shutil.copytree(src, dest)
print(f"backup -> {bak}")

# Ensure KEEP workspaceStorage exists with correct folder URI
keep_dir = ws / keep
keep_dir.mkdir(parents=True, exist_ok=True)
(keep_dir / "workspace.json").write_text(
    json.dumps({"folder": "file://" + to}, indent=2) + "\n"
)
if not (keep_dir / "state.vscdb").exists():
    # Minimal empty DB Cursor can reopen
    kdb = sqlite3.connect(keep_dir / "state.vscdb")
    kdb.execute("CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)")
    kdb.commit()
    kdb.close()

target_uri = {
    "$mid": 1,
    "fsPath": to,
    "external": "file://" + to,
    "path": to,
    "scheme": "file",
}
target_wi = {"id": keep, "uri": target_uri}
source_ids = {move_from, *also_drop}

con = sqlite3.connect(gdb)
con.execute("PRAGMA busy_timeout=5000")

# 1) SQL composerHeaders: any header on Stambha path or source ids → KEEP
moved = 0
for cid, wid, val in con.execute("SELECT composerId, workspaceId, value FROM composerHeaders"):
    blob = json.loads(val) if val else {}
    wi = blob.get("workspaceIdentifier") or {}
    uri = wi.get("uri") or {}
    fp = (uri.get("fsPath") or uri.get("path") or "").rstrip("/")
    hit = wid in source_ids or wid == keep or fp == to
    if not hit:
        continue
    if wid == keep and (wi.get("id") == keep):
        continue
    blob["workspaceIdentifier"] = dict(target_wi)
    blob["workspaceIdentifier"]["uri"] = dict(target_uri)
    con.execute(
        "UPDATE composerHeaders SET workspaceId=?, value=? WHERE composerId=?",
        (keep, json.dumps(blob, separators=(",", ":")), cid),
    )
    moved += 1
print(f"SQL headers retargeted -> {keep[:8]}: {moved}")

# 2) ItemTable composer.composerHeaders
row = con.execute("SELECT value FROM ItemTable WHERE key='composer.composerHeaders'").fetchone()
if not row:
    raise SystemExit("composer.composerHeaders missing")
headers = json.loads(row[0])
changed = 0
for c in headers.get("allComposers", []):
    wi = c.get("workspaceIdentifier") or {}
    uri = wi.get("uri") or {}
    fp = (uri.get("fsPath") or uri.get("path") or "").rstrip("/")
    if fp == to or wi.get("id") in source_ids | {keep}:
        c["workspaceIdentifier"] = dict(target_wi)
        c["workspaceIdentifier"]["uri"] = dict(target_uri)
        changed += 1
con.execute(
    "UPDATE ItemTable SET value=? WHERE key='composer.composerHeaders'",
    (json.dumps(headers, separators=(",", ":")),),
)
print(f"ItemTable headers updated: {changed}")

ids = []
for c in headers.get("allComposers", []):
    if (c.get("workspaceIdentifier") or {}).get("id") == keep and c.get("composerId"):
        ids.append(c["composerId"])
for (cid,) in con.execute("SELECT composerId FROM composerHeaders WHERE workspaceId=?", (keep,)):
    if cid not in ids:
        ids.append(cid)

# 3) composerData blobs
cd_changed = 0
for cid in ids:
    key = f"composerData:{cid}"
    r = con.execute("SELECT value FROM cursorDiskKV WHERE key=?", (key,)).fetchone()
    table = "cursorDiskKV"
    if not r:
        r = con.execute("SELECT value FROM ItemTable WHERE key=?", (key,)).fetchone()
        table = "ItemTable"
    if not r:
        continue
    raw = r[0]
    try:
        blob = json.loads(raw if isinstance(raw, str) else raw.decode())
    except Exception:
        continue
    blob["workspaceIdentifier"] = {"id": keep, "uri": dict(target_uri)}
    out = json.dumps(blob, separators=(",", ":"))
    if table == "cursorDiskKV":
        con.execute("UPDATE cursorDiskKV SET value=? WHERE key=?", (out, key))
    else:
        con.execute("UPDATE ItemTable SET value=? WHERE key=?", (out, key))
    cd_changed += 1
print(f"composerData rewritten: {cd_changed}")

# 4) glass.localAgentProjects.v1
row = con.execute("SELECT value FROM ItemTable WHERE key='glass.localAgentProjects.v1'").fetchone()
glass_n = 0
if row:
    projects = json.loads(row[0])
    for p in projects:
        w = p.get("workspace") or {}
        uri = w.get("uri") or {}
        fp = (uri.get("fsPath") or uri.get("path") or "").rstrip("/")
        if w.get("id") in source_ids | {keep} or fp == to:
            w["id"] = keep
            w["uri"] = dict(target_uri)
            p["workspace"] = w
            glass_n += 1
    con.execute(
        "UPDATE ItemTable SET value=? WHERE key='glass.localAgentProjects.v1'",
        (json.dumps(projects, separators=(",", ":")),),
    )
print(f"glass.localAgentProjects updated: {glass_n}")

# 5) Transfer glass.tabs / slash-menu keys from MOVE_FROM → KEEP
key_moved = 0
for (key, val) in list(con.execute("SELECT key, value FROM ItemTable")):
    new_key = None
    if f"cursor/glass.tabs.v2/{move_from}" in key:
        new_key = key.replace(move_from, keep, 1)
    elif key.endswith(f"glass.{move_from}") or f".glass.{move_from}" in key:
        new_key = key.replace(move_from, keep, 1)
    elif f"slashMenuItems.v2.local.glass.{move_from}" in key:
        new_key = key.replace(move_from, keep, 1)
    if not new_key or new_key == key:
        continue
    exists = con.execute("SELECT 1 FROM ItemTable WHERE key=?", (new_key,)).fetchone()
    if exists:
        con.execute("DELETE FROM ItemTable WHERE key=?", (key,))
    else:
        con.execute("UPDATE ItemTable SET key=? WHERE key=?", (new_key, key))
    key_moved += 1
print(f"glass ItemTable keys moved: {key_moved}")

# 6) glassSidebarSettings sectionOrder: rewrite workspace:MOVE_FROM → workspace:KEEP
row = con.execute("SELECT value FROM ItemTable WHERE key='cursor/glassSidebarSettings'").fetchone()
if row:
    settings = json.loads(row[0])
    order = (settings.get("sectionOrderByGroupBy") or {}).get("repository") or []
    new_order = []
    seen = set()
    for item in order:
        if item == f"workspace:{move_from}":
            item = f"workspace:{keep}"
        for drop in also_drop:
            if item == f"workspace:{drop}":
                item = f"workspace:{keep}"
        if item in seen:
            continue
        seen.add(item)
        new_order.append(item)
    if "workspace:"+keep not in seen:
        new_order.insert(0, "workspace:"+keep)
    settings.setdefault("sectionOrderByGroupBy", {})["repository"] = new_order
    con.execute(
        "UPDATE ItemTable SET value=? WHERE key='cursor/glassSidebarSettings'",
        (json.dumps(settings, separators=(",", ":")),),
    )
    print("glassSidebarSettings sectionOrder rewritten")

# 7) workspaceMetadata.entries — keep one Stambha row on KEEP, drop duplicates
row = con.execute("SELECT value FROM ItemTable WHERE key='workspaceMetadata.entries'").fetchone()
if row:
    wrap = json.loads(row[0])
    entries = wrap.get("entries") if isinstance(wrap, dict) else wrap
    kept_meta = None
    out_entries = []
    for e in entries:
        wid = e.get("workspaceId") or ""
        folder = (e.get("folderUri") or "").rstrip("/")
        is_stambha = folder == ("file://" + to) or wid in source_ids | {keep} or wid in also_drop
        if not is_stambha:
            out_entries.append(e)
            continue
        if kept_meta is None:
            e = dict(e)
            e["workspaceId"] = keep
            e["folderUri"] = "file://" + to
            e["displayPath"] = "~/Documents/Arpit/_Others/GitHub/Stambha"
            kept_meta = e
            out_entries.append(e)
        # else drop duplicate
    if isinstance(wrap, dict):
        wrap["entries"] = out_entries
        payload = wrap
    else:
        payload = out_entries
    con.execute(
        "UPDATE ItemTable SET value=? WHERE key='workspaceMetadata.entries'",
        (json.dumps(payload, separators=(",", ":")),),
    )
    print("workspaceMetadata.entries deduped for Stambha")

# 8) Merge workspace UI prefs: MOVE_FROM → KEEP
src_db = ws / move_from / "state.vscdb"
keep_db = ws / keep / "state.vscdb"
if src_db.exists() and keep_db.exists():
    scon = sqlite3.connect(src_db)
    kcon = sqlite3.connect(keep_db)
    sr = scon.execute("SELECT value FROM ItemTable WHERE key='composer.composerData'").fetchone()
    kr = kcon.execute("SELECT value FROM ItemTable WHERE key='composer.composerData'").fetchone()
    kdata = json.loads(kr[0]) if kr else {}
    if sr:
        sdata = json.loads(sr[0])
        # Prefer named Stambha chat selection from source
        if sdata.get("selectedComposerIds"):
            kdata["selectedComposerIds"] = sdata["selectedComposerIds"]
            kdata["lastFocusedComposerIds"] = sdata.get("lastFocusedComposerIds") or sdata["selectedComposerIds"]
    if not kdata.get("selectedComposerIds") and ids:
        prefer = "1302c71f-79e9-459e-8148-6b9fda2e7e1b"
        pick = prefer if prefer in ids else ids[0]
        kdata["selectedComposerIds"] = [pick]
        kdata["lastFocusedComposerIds"] = [pick]
    kdata["hasMigratedComposerData"] = True
    kdata["hasMigratedMultipleComposers"] = True
    kdata.pop("allComposers", None)
    kcon.execute(
        "INSERT OR REPLACE INTO ItemTable(key, value) VALUES('composer.composerData', ?)",
        (json.dumps(kdata, separators=(",", ":")),),
    )
    for (key,) in scon.execute(
        "SELECT key FROM ItemTable WHERE key LIKE 'agentSidebar%' OR key LIKE 'ideSidebar%' OR key LIKE 'cursor/agentLayout%'"
    ):
        if kcon.execute("SELECT 1 FROM ItemTable WHERE key=?", (key,)).fetchone():
            continue
        val = scon.execute("SELECT value FROM ItemTable WHERE key=?", (key,)).fetchone()[0]
        kcon.execute("INSERT INTO ItemTable(key, value) VALUES(?, ?)", (key, val))
    kcon.commit()
    scon.close()
    kcon.close()
    print("merged workspace UI prefs into KEEP")

con.commit()
con.execute("PRAGMA wal_checkpoint(TRUNCATE)")
con.close()

# 9) Orphan + remove leftover workspaceStorage dirs (not KEEP)
removed = []
for wid in [move_from, *also_drop]:
    p = ws / wid
    if not p.exists():
        continue
    # Orphan first (safety), then remove
    wj = p / "workspace.json"
    if wj.exists():
        wj.write_text(
            json.dumps(
                {"folder": f"file://{to}.__rebind_orphan_{wid[:8]}"},
                indent=2,
            )
            + "\n"
        )
    shutil.rmtree(p)
    removed.append(wid)
print("removed workspaceStorage:", removed)

con = sqlite3.connect(f"file:{gdb}?mode=ro", uri=True)
print(
    "verify KEEP named=",
    con.execute(
        "SELECT count(*) FROM composerHeaders WHERE workspaceId=? AND ifnull(json_extract(value,'$.name'),'')!=''",
        (keep,),
    ).fetchone()[0],
)
print(
    "verify MOVE_FROM remaining=",
    con.execute("SELECT count(*) FROM composerHeaders WHERE workspaceId=?", (move_from,)).fetchone()[0],
)
print("KEEP folder:", (ws / keep / "workspace.json").read_text().strip())
print("MOVE_FROM exists?", (ws / move_from).exists())
print("Done. Reopen Cursor on:", to)
print("Expected: IDE Agents sidebar lists Discord / export / v1.2.0 chats.")
PY
