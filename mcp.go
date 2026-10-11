package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// A small Model Context Protocol server over Streamable HTTP (JSON responses only).
// Clients authenticate with an API token: Authorization: Bearer <token>. Every tool
// acts as the user who made the token, with that user's folder permissions.

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }

var readOnly = map[string]any{"readOnlyHint": true}

var mcpTools = []mcpTool{
	{Name: "search_files", Annotations: readOnly,
		Description: "Search the NAS index by file or folder name. Every word must appear somewhere in the path (substring match, case-insensitive). Prefix a word with - to exclude it. Supports ext:pdf and type:video inside the query.",
		InputSchema: obj(map[string]any{
			"query":          str("Words to match in the path, e.g. 'taxes 2024 -draft' or 'ext:stl bracket'"),
			"type":           map[string]any{"type": "string", "enum": []string{"image", "video", "audio", "doc", "archive", "code", "3d", "folder", "other"}},
			"folder":         str("Only search inside this library path, e.g. 'DOCUMENTS/Taxes'"),
			"modified_after": str("ISO date (YYYY-MM-DD); only items modified on or after it"),
			"min_size_mb":    num("Minimum size in MB"),
			"sort":           map[string]any{"type": "string", "enum": []string{"relevance", "newest", "oldest", "largest", "name"}},
			"limit":          num("Max results (default 50, max 500)"),
		})},
	{Name: "list_directory", Annotations: readOnly,
		Description: "List a folder. Use an empty path to list the library roots.",
		InputSchema: obj(map[string]any{"path": str("Library path, e.g. 'PROJECTS/website'")})},
	{Name: "file_info", Annotations: readOnly,
		Description: "Details for one file or folder, including any share links that point at it.",
		InputSchema: obj(map[string]any{"path": str("Library path")}, "path")},
	{Name: "read_text_file", Annotations: readOnly,
		Description: "Read the start of a text file (code, notes, csv, logs). Binary files are refused.",
		InputSchema: obj(map[string]any{"path": str("Library path"), "max_kb": num("How much to read in KB (default 64, max 512)")}, "path")},
	{Name: "recent_changes", Annotations: readOnly,
		Description: "Files added, modified or deleted on the NAS, newest first.",
		InputSchema: obj(map[string]any{
			"hours":  num("Look back this many hours (default 24)"),
			"folder": str("Only changes inside this library path"),
			"kind":   map[string]any{"type": "string", "enum": []string{"added", "modified", "deleted"}},
			"limit":  num("Max results (default 100)"),
		})},
	{Name: "create_share_link",
		Description: "Create a public share link for a file or folder. Returns the URL and a QR code URL. Ask the user before sharing anything sensitive.",
		InputSchema: obj(map[string]any{
			"path":          str("Library path to share"),
			"expires_hours": num("Hours until the link stops working. 0 uses the default; -1 means never."),
			"password":      str("Optional password the recipient must enter"),
			"max_downloads": num("Optional download limit"),
			"note":          str("Optional note shown to the recipient"),
			"label":         str("Optional label for your own list of links"),
		}, "path")},
	{Name: "create_file_drop",
		Description: "Create an upload-only link so someone can send files to the NAS without seeing anything on it.",
		InputSchema: obj(map[string]any{
			"label":         str("Name for the drop; also the folder name uploads land in"),
			"expires_hours": num("Hours until the link stops working. 0 uses the default; -1 means never."),
			"password":      str("Optional password"),
			"note":          str("Optional note shown on the upload page"),
		}, "label")},
	{Name: "create_shared_folder",
		Description: "Create a shared folder: one link whose contents you can keep changing. Add library files or folders now or later with add_to_shared_folder; the link and password stay the same.",
		InputSchema: obj(map[string]any{
			"label":         str("Name shown to the recipient"),
			"paths":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Library paths to include"},
			"expires_hours": num("Hours until the link stops working. 0 uses the default; -1 means never."),
			"password":      str("Optional password"),
			"note":          str("Optional note shown to the recipient"),
			"allow_uploads": map[string]any{"type": "boolean", "description": "Let recipients add files too"},
		}, "label")},
	{Name: "add_to_shared_folder",
		Description: "Add library files or folders to an existing shared folder. Recipients see them immediately on the same link.",
		InputSchema: obj(map[string]any{"id": str("Shared folder id (the part after /s/)"), "paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "id", "paths")},
	{Name: "remove_from_shared_folder",
		Description: "Remove a library path from a shared folder. Nothing on disk is deleted.",
		InputSchema: obj(map[string]any{"id": str("Shared folder id"), "path": str("Library path to remove")}, "id", "path")},
	{Name: "list_share_links", Annotations: readOnly,
		Description: "List share links with their state (active, expired, revoked, used-up, missing) and download counts.",
		InputSchema: obj(map[string]any{"active_only": map[string]any{"type": "boolean"}, "path": str("Only links for this library path")})},
	{Name: "revoke_share_link",
		Description: "Turn off a share link immediately.",
		InputSchema: obj(map[string]any{"id": str("Share id (the part after /s/)")}, "id")},
	{Name: "index_status", Annotations: readOnly,
		Description: "How many files and folders are indexed, total size, and whether a scan is running (totals for admins only).",
		InputSchema: obj(map[string]any{})},
}

// adminTools are left out of tools/list for other users (and refused if called anyway).
var adminTools = map[string]bool{"create_file_drop": true}

func (a *App) handleMCP(w http.ResponseWriter, r *http.Request) {
	u, tid := a.tokenUser(r)
	if u == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="burrow"`)
		httpErr(w, http.StatusUnauthorized, "API token required (create one in Settings)")
		return
	}
	r = withAuth(r, authInfo{user: u, viaAPI: true, tokenID: tid})
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpErr(w, http.StatusMethodNotAllowed, "POST JSON-RPC messages to this endpoint")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpErr(w, 400, "bad body")
		return
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) > 0 && body[0] == '[' {
		var reqs []rpcReq
		if err := json.Unmarshal(body, &reqs); err != nil {
			writeRPC(w, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}})
			return
		}
		var out []rpcResp
		for _, q := range reqs {
			if resp, ok := a.mcpDispatch(r, q); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	var q rpcReq
	if err := json.Unmarshal(body, &q); err != nil {
		writeRPC(w, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}})
		return
	}
	resp, ok := a.mcpDispatch(r, q)
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeRPC(w, resp)
}

func writeRPC(w http.ResponseWriter, resp rpcResp) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// mcpDispatch handles one message; ok=false for notifications (no response).
func (a *App) mcpDispatch(r *http.Request, q rpcReq) (rpcResp, bool) {
	if len(q.ID) == 0 {
		return rpcResp{}, false
	}
	resp := rpcResp{JSONRPC: "2.0", ID: q.ID}
	switch q.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(q.Params, &p)
		pv := p.ProtocolVersion
		if pv == "" {
			pv = "2025-06-18"
		}
		resp.Result = map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "burrow", "version": version},
			"instructions":    "Burrow indexes a NAS. Paths are library paths like 'DOCUMENTS/Taxes/2024.pdf'. Search first, then use file_info or read_text_file. Only create share links the user asked for.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		tools := []mcpTool{}
		for _, t := range mcpTools {
			if !adminTools[t.Name] || userOf(r).Admin {
				tools = append(tools, t)
			}
		}
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(q.Params, &p); err != nil {
			resp.Error = &rpcErr{-32602, "invalid params"}
			break
		}
		out, err := a.mcpCall(r, p.Name, p.Arguments)
		if err != nil {
			resp.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "Error: " + err.Error()}}, "isError": true}
			break
		}
		b, _ := json.MarshalIndent(out, "", " ")
		resp.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "structuredContent": wrapStructured(out)}
	default:
		resp.Error = &rpcErr{-32601, "method not found: " + q.Method}
	}
	return resp, true
}

// structuredContent must be a JSON object.
func wrapStructured(v any) any {
	b, _ := json.Marshal(v)
	if len(b) > 0 && b[0] == '{' {
		return v
	}
	return map[string]any{"result": v}
}

func argS(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func argF(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case string:
		var f float64
		_, _ = fmt.Sscanf(v, "%g", &f)
		return f
	}
	return 0
}

type mcpEntry struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	Size     string `json:"size,omitempty"`
	Modified string `json:"modified"`
	Shared   int    `json:"active_links,omitempty"`
}

func toMCP(es []Entry) []mcpEntry {
	out := make([]mcpEntry, 0, len(es))
	for _, e := range es {
		t := e.Kind
		if t == "" {
			t = "file"
		}
		m := mcpEntry{Path: e.Path, Type: t, Modified: time.Unix(e.Mtime, 0).Format("2006-01-02 15:04"), Shared: e.Shared}
		if !e.IsDir {
			m.Size = humanSize(e.Size)
		}
		out = append(out, m)
	}
	return out
}

func (a *App) mcpCall(r *http.Request, name string, args map[string]any) (any, error) {
	if args == nil {
		args = map[string]any{}
	}
	u := userOf(r)
	if adminTools[name] && !u.Admin {
		return nil, errAdminsOnly
	}
	switch name {
	case "search_files":
		sq := SearchQuery{Q: argS(args, "query"), Kind: argS(args, "type"), Root: argS(args, "folder"), Sort: argS(args, "sort"), Limit: int(argF(args, "limit"))}
		if sq.Limit <= 0 {
			sq.Limit = 50
		}
		if sq.Limit > 500 {
			sq.Limit = 500
		}
		if d := argS(args, "modified_after"); d != "" {
			t, err := time.ParseInLocation("2006-01-02", d, time.Local)
			if err != nil {
				return nil, fmt.Errorf("modified_after must be YYYY-MM-DD")
			}
			sq.After = t.Unix()
		}
		if mb := argF(args, "min_size_mb"); mb > 0 {
			sq.MinSize = int64(mb * 1024 * 1024)
		}
		res, err := a.search(u, sq)
		if err != nil {
			return nil, err
		}
		return map[string]any{"total_matches": res.Total, "returned": len(res.Entries), "results": toMCP(res.Entries)}, nil

	case "list_directory":
		es, err := a.listDir(u, argS(args, "path"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": cleanRel(argS(args, "path")), "entries": toMCP(es)}, nil

	case "file_info":
		abs, rel, err := a.resolve(argS(args, "path"))
		if err == nil && !u.canReach(rel, permRead) {
			err = errNotAllowed
		}
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		e := entryFromInfo(rel, info)
		shares, _ := a.listShares(r, u, rel, false)
		out := map[string]any{"entry": toMCP([]Entry{e})[0], "bytes": e.Size, "share_links": shares, "you_can_share": u.can(rel, permShare)}
		if e.IsDir {
			var n, sz int64
			cond, cargs := u.scope("path", permRead)
			_ = a.st.db.QueryRow(`SELECT count(*), coalesce(sum(size),0) FROM files WHERE path >= ? AND path < ? AND `+cond, append([]any{rel + "/", rel + "0"}, cargs...)...).Scan(&n, &sz)
			out["indexed_items_inside"] = n
			out["total_size"] = humanSize(sz)
		}
		return out, nil

	case "read_text_file":
		abs, _, err := a.resolveFor(u, argS(args, "path"), permRead)
		if err != nil {
			return nil, err
		}
		kb := argF(args, "max_kb")
		if kb <= 0 {
			kb = 64
		}
		if kb > 512 {
			kb = 512
		}
		f, err := os.Open(abs)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, _ := f.Stat()
		if st.IsDir() {
			return nil, fmt.Errorf("that is a folder")
		}
		buf := make([]byte, int(kb*1024))
		n, _ := io.ReadFull(f, buf)
		buf = buf[:n]
		if strings.ContainsRune(string(buf[:min(n, 8000)]), 0) {
			return nil, fmt.Errorf("binary file; not readable as text")
		}
		for !utf8.Valid(buf) && len(buf) > 0 {
			buf = buf[:len(buf)-1]
		}
		return map[string]any{"size": st.Size(), "truncated": int64(n) < st.Size(), "text": string(buf)}, nil

	case "recent_changes":
		h := argF(args, "hours")
		if h <= 0 {
			h = 24
		}
		lim := int(argF(args, "limit"))
		if lim <= 0 {
			lim = 100
		}
		cs, err := a.changes(u, now()-int64(h*3600), argS(args, "folder"), argS(args, "kind"), lim)
		if err != nil {
			return nil, err
		}
		type c struct {
			When, Kind, Path, Size string
		}
		out := []c{}
		for _, x := range cs {
			s := ""
			if !x.IsDir && x.Kind != "deleted" {
				s = humanSize(x.Size)
			}
			out = append(out, c{time.Unix(x.TS, 0).Format("2006-01-02 15:04"), x.Kind, x.Path, s})
		}
		return map[string]any{"changes": out}, nil

	case "create_share_link":
		in := ShareInput{Path: argS(args, "path"), Mode: "link", Password: argS(args, "password"), Note: argS(args, "note"), Label: argS(args, "label"), ExpiresHours: argF(args, "expires_hours"), MaxDownloads: int64(argF(args, "max_downloads"))}
		s, err := a.createShare(r, u, in)
		if err != nil {
			return nil, err
		}
		return shareSummary(s), nil

	case "create_file_drop":
		in := ShareInput{Mode: "drop", Label: argS(args, "label"), Password: argS(args, "password"), Note: argS(args, "note"), ExpiresHours: argF(args, "expires_hours")}
		s, err := a.createShare(r, u, in)
		if err != nil {
			return nil, err
		}
		return shareSummary(s), nil

	case "create_shared_folder":
		up, _ := args["allow_uploads"].(bool)
		in := ShareInput{Mode: "collection", Label: argS(args, "label"), Password: argS(args, "password"), Note: argS(args, "note"), ExpiresHours: argF(args, "expires_hours"), AllowUpload: up, Items: argList(args, "paths")}
		s, err := a.createShare(r, u, in)
		if err != nil {
			return nil, err
		}
		return shareSummary(s), nil

	case "add_to_shared_folder", "remove_from_shared_folder":
		s, err := a.ownedShare(u, argS(args, "id"))
		if err != nil || s.Mode != "collection" {
			return nil, fmt.Errorf("no shared folder with that id")
		}
		if name == "add_to_shared_folder" {
			owner := a.shareOwner(s)
			if owner == nil {
				return nil, fmt.Errorf("this link's owner is no longer active")
			}
			if err := a.addItems(owner, s.ID, argList(args, "paths")); err != nil {
				return nil, err
			}
		} else {
			_, _ = a.st.exec(`DELETE FROM share_items WHERE share_id=? AND path=?`, s.ID, cleanRel(argS(args, "path")))
		}
		return map[string]any{"id": s.ID, "items": a.itemPaths(s.ID)}, nil

	case "list_share_links":
		active, _ := args["active_only"].(bool)
		ss, err := a.listShares(r, u, argS(args, "path"), active)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, s := range ss {
			out = append(out, shareSummary(s))
		}
		return map[string]any{"links": out}, nil

	case "revoke_share_link":
		t := true
		if err := a.patchShare(u, argS(args, "id"), SharePatch{Revoked: &t}); err != nil {
			return nil, fmt.Errorf("no share with that id")
		}
		return map[string]any{"revoked": argS(args, "id")}, nil

	case "index_status":
		s := a.idx.Status()
		if !u.Admin {
			return map[string]any{"scanning": s.Scanning, "last_scan": time.Unix(s.LastEnd, 0).Format(time.RFC3339)}, nil
		}
		return map[string]any{"files": s.Files, "folders": s.Dirs, "total_size": humanSize(s.Bytes), "scanning": s.Scanning, "last_scan": time.Unix(s.LastEnd, 0).Format(time.RFC3339), "live_updates": s.Watching, "note": s.WatchNote}, nil
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

func argList(m map[string]any, k string) []string {
	var out []string
	if l, ok := m[k].([]any); ok {
		for _, v := range l {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

func shareSummary(s Share) map[string]any {
	m := map[string]any{"id": s.ID, "url": s.URL, "qr_png": s.URL + "/qr.png", "path": s.Path, "mode": s.Mode, "state": s.State, "downloads": s.DL, "password": s.HasPW}
	if s.Expires > 0 {
		m["expires"] = time.Unix(s.Expires, 0).Format("2006-01-02 15:04")
	} else {
		m["expires"] = "never"
	}
	if s.MaxDL > 0 {
		m["max_downloads"] = s.MaxDL
	}
	if s.Label != "" {
		m["label"] = s.Label
	}
	if s.Mode == "collection" {
		m["items"] = s.Items
		m["recipients_can_upload"] = s.AllowUp
	}
	return m
}
