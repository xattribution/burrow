package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcpClient makes token as the signed-in user (with scope, or the default when nil)
// and returns a function that calls MCP methods with it.
func (e *tenv) mcpClient(scope map[string]any) (string, func(method string, params any) map[string]any) {
	e.t.Helper()
	body := map[string]any{"name": "test"}
	if scope != nil {
		body["scope"] = scope
	}
	var tok struct{ Token string }
	_ = json.Unmarshal(e.do("POST", "/api/tokens", body, 200), &tok)
	call := func(method string, params any) map[string]any {
		e.t.Helper()
		msg := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
		if params != nil {
			msg["params"] = params
		}
		_, b := e.bearer(tok.Token, "POST", "/mcp", msg)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		res, _ := out["result"].(map[string]any)
		if res == nil {
			e.t.Fatalf("%s: %s", method, b)
		}
		return res
	}
	return tok.Token, call
}

func toolCall(name string, args map[string]any) map[string]any {
	return map[string]any{"name": name, "arguments": args}
}

func resultText(res map[string]any) string {
	return res["content"].([]any)[0].(map[string]any)["text"].(string)
}

// listedTools maps each offered tool to whether it is marked ask-every-time.
func listedTools(t *testing.T, res map[string]any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, x := range res["tools"].([]any) {
		m := x.(map[string]any)
		meta, _ := m["_meta"].(map[string]any)
		out[m["name"].(string)] = meta["anthropic/requiresUserInteraction"] == true
	}
	return out
}

func TestTokenScopeOffersOnlyChosenTools(t *testing.T) {
	e := setup(t)
	tok, call := e.mcpClient(map[string]any{
		"tools": map[string]string{"search_files": "ask", "list_directory": "on", "create_share_link": "off"},
	})
	tools := listedTools(t, call("tools/list", nil))
	if len(tools) != 2 || !tools["search_files"] || tools["list_directory"] {
		t.Fatalf("tools offered (name: asks): %v", tools)
	}
	if res := call("tools/call", toolCall("create_share_link", map[string]any{"path": "DOCS/notes.txt"})); res["isError"] != true || !strings.Contains(resultText(res), "doesn't offer") {
		t.Fatalf("a tool that is off ran: %v", res)
	}
	if res := call("tools/call", toolCall("search_files", map[string]any{"query": "notes"})); !strings.Contains(resultText(res), "DOCS/notes.txt") {
		t.Fatalf("search: %v", res)
	}
	// made for MCP only: the REST API refuses it
	if c, _ := e.bearer(tok, "GET", "/api/ls?path=", nil); c != 403 {
		t.Fatalf("MCP-only token on the API: %d", c)
	}

	// turning the API on lets it read there, but it still can't share or edit
	var toks []tokenView
	_ = json.Unmarshal(e.do("GET", "/api/tokens", nil, 200), &toks)
	e.do("PATCH", "/api/tokens/"+itoa(toks[0].ID), map[string]any{"scope": map[string]any{"tools": map[string]string{"search_files": "ask"}, "api": true}}, 200)
	if c, _ := e.bearer(tok, "GET", "/api/ls?path=DOCS", nil); c != 200 {
		t.Fatalf("API read: %d", c)
	}
	if c, b := e.bearer(tok, "POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt"}); c == 200 {
		t.Fatalf("read-only token shared through the API: %s", b)
	}
	if c, b := e.bearer(tok, "POST", "/api/fs/mkdir", map[string]any{"path": "DOCS/made"}); c == 200 {
		t.Fatalf("read-only token made a folder through the API: %s", b)
	}
	if c, _ := e.bearer(tok, "POST", "/api/rescan", nil); c != 403 {
		t.Fatalf("admin action without admin tools: %d", c)
	}
	// a token can't change tokens
	if c, _ := e.bearer(tok, "PATCH", "/api/tokens/"+itoa(toks[0].ID), map[string]any{"scope": map[string]any{"api": true}}); c != 401 {
		t.Fatalf("token edited a token: %d", c)
	}
}

func TestTokenScopeFolders(t *testing.T) {
	e := setup(t)
	var other Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt"}, 200), &other)
	all := map[string]string{}
	for _, tl := range mcpTools {
		all[tl.Name] = "ask"
	}
	delete(all, "create_file_drop") // no admin powers
	_, call := e.mcpClient(map[string]any{"tools": all, "folders": []string{"DOCS/Taxes"}})
	tc := func(name string, args map[string]any) map[string]any { return call("tools/call", toolCall(name, args)) }

	if s := resultText(tc("search_files", map[string]any{"query": "notes"})); strings.Contains(s, "notes.txt") {
		t.Fatalf("search outside the token's folders: %s", s)
	}
	if s := resultText(tc("search_files", map[string]any{"query": "2024"})); !strings.Contains(s, "DOCS/Taxes/2024-return.pdf") {
		t.Fatalf("search inside: %s", s)
	}
	if s := resultText(tc("list_directory", map[string]any{"path": ""})); !strings.Contains(s, `"DOCS"`) || strings.Contains(s, "PROJECTS") {
		t.Fatalf("libraries: %s", s)
	}
	if s := resultText(tc("list_directory", map[string]any{"path": "DOCS"})); !strings.Contains(s, "DOCS/Taxes") || strings.Contains(s, "notes.txt") {
		t.Fatalf("walking down to the token's folder: %s", s)
	}
	if res := tc("read_text_file", map[string]any{"path": "DOCS/notes.txt"}); res["isError"] != true {
		t.Fatal("read outside the token's folders")
	}
	if s := resultText(tc("list_share_links", nil)); strings.Contains(s, other.ID) {
		t.Fatal("listed a link outside the token's folders")
	}
	if res := tc("revoke_share_link", map[string]any{"id": other.ID}); res["isError"] != true {
		t.Fatal("revoked a link outside the token's folders")
	}
	if s := resultText(tc("create_share_link", map[string]any{"path": "DOCS/Taxes/2024-return.pdf"})); !strings.Contains(s, "/s/") {
		t.Fatalf("share inside: %s", s)
	}
	if res := tc("create_shared_folder", map[string]any{"label": "Mixed", "paths": []string{"DOCS/Taxes", "DOCS/notes.txt"}}); res["isError"] != true {
		t.Fatal("shared folder took a path outside the token's folders")
	}
	if s := resultText(tc("index_status", nil)); strings.Contains(s, "total_size") {
		t.Fatalf("admin totals without admin tools: %s", s)
	}
	if _, ok := listedTools(t, call("tools/list", nil))["create_file_drop"]; ok {
		t.Fatal("drop tool offered without admin tools")
	}
	// a token can only be limited to folders the account can see
	e.do("POST", "/api/tokens", map[string]any{"scope": map[string]any{"folders": []string{"PRIVATE"}}}, 400)
}

func TestTokenEditTools(t *testing.T) {
	e := setup(t)
	_, call := e.mcpClient(nil) // the default: every tool, asking each time
	tools := listedTools(t, call("tools/list", nil))
	for _, name := range []string{"create_folder", "write_text_file", "move_item", "delete_item", "create_file_drop"} {
		if ask, ok := tools[name]; !ok || !ask {
			t.Fatalf("%s offered %v, asks %v", name, ok, ask)
		}
	}
	tc := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res := call("tools/call", toolCall(name, args))
		if res["isError"] == true {
			t.Fatalf("%s: %s", name, resultText(res))
		}
		return res
	}
	tc("create_folder", map[string]any{"path": "PROJECTS/notes/2026", "parents": true})
	if fi, err := os.Stat(filepath.Join(e.data, "PROJECTS/notes/2026")); err != nil || !fi.IsDir() {
		t.Fatal("folder not made")
	}
	tc("write_text_file", map[string]any{"path": "PROJECTS/notes/2026/todo.md", "text": "one"})
	if res := call("tools/call", toolCall("write_text_file", map[string]any{"path": "PROJECTS/notes/2026/todo.md", "text": "two"})); res["isError"] != true {
		t.Fatal("replaced a file without overwrite")
	}
	tc("write_text_file", map[string]any{"path": "PROJECTS/notes/2026/todo.md", "text": "two", "overwrite": true})
	if got := readFile(t, filepath.Join(e.data, "PROJECTS/notes/2026/todo.md")); got != "two" {
		t.Fatalf("content: %q", got)
	}
	tc("move_item", map[string]any{"from": "PROJECTS/notes/2026/todo.md", "to": "PROJECTS/notes/done.md"})
	tc("delete_item", map[string]any{"path": "PROJECTS/notes/done.md"})
	if _, err := os.Stat(filepath.Join(e.data, "PROJECTS/notes/done.md")); !os.IsNotExist(err) {
		t.Fatal("deleted file still there")
	}
	var trash []TrashItem
	_ = json.Unmarshal(e.do("GET", "/api/trash", nil, 200), &trash)
	if len(trash) != 2 { // the replaced version and the deleted file
		t.Fatalf("trash: %+v", trash)
	}
	if res := call("tools/call", toolCall("write_text_file", map[string]any{"path": "PROJECTS/.burrow-x", "text": "x"})); res["isError"] != true {
		t.Fatal("wrote a reserved name")
	}
}

func TestTokenScopeForNonAdmins(t *testing.T) {
	e := setup(t)
	e.mkUser("bob", false, bobGrants...)
	bob := e.as("bob", "longenough-bob")
	var catalog []struct{ Name string }
	_ = json.Unmarshal(bob.do("GET", "/api/tokens/tools", nil, 200), &catalog)
	for _, c := range catalog {
		if c.Name == "create_file_drop" {
			t.Fatal("admin tool in a non-admin's catalog")
		}
	}
	_, call := bob.mcpClient(map[string]any{"tools": map[string]string{"create_file_drop": "on", "search_files": "on"}})
	if tools := listedTools(t, call("tools/list", nil)); len(tools) != 1 {
		t.Fatalf("tools: %v", tools)
	}
	// someone else's token can't be changed
	var toks []tokenView
	_ = json.Unmarshal(bob.do("GET", "/api/tokens", nil, 200), &toks)
	e.do("PATCH", "/api/tokens/"+itoa(toks[0].ID), map[string]any{"name": "mine now"}, 404)
}

func TestTokensFromBeforeScopes(t *testing.T) {
	e := setup(t)
	tok, call := e.mcpClient(nil)
	_, _ = e.app.st.exec(`UPDATE tokens SET scope=''`)
	tools := listedTools(t, call("tools/list", nil))
	if len(tools) != len(mcpTools) {
		t.Fatalf("old token offers %d of %d tools", len(tools), len(mcpTools))
	}
	for name, ask := range tools {
		if !ask {
			t.Fatalf("%s doesn't ask", name)
		}
	}
	if c, _ := e.bearer(tok, "GET", "/api/ls?path=", nil); c != 200 {
		t.Fatalf("old token on the API: %d", c)
	}
}
