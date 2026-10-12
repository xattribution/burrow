package main

// API tokens and what each one may do. A token acts as the user who made it,
// narrowed by its scope: which MCP tools it offers (and whether the client must ask
// before each call), which folders it reaches, and whether scripts may use it on
// the REST API too. The narrowing is applied to the user itself (User.lim, and no
// admin powers without the admin tools), so every check that guards the web UI
// guards a token as well, on MCP and on the API.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// How a scope offers a tool. A tool missing from a scope is off, so tools added in
// later versions stay off for existing tokens until someone turns them on.
const (
	toolOff = "off" // not offered
	toolAsk = "ask" // offered; MCP clients that support it ask the user before every call
	toolOn  = "on"  // offered; the client's own permission settings decide
)

// What a tool needs from the account.
const (
	groupRead  = "read"
	groupShare = "share"
	groupEdit  = "edit"
	groupAdmin = "admin" // admin accounts only
)

// askMeta marks a tool so Claude Code asks before every call, in every permission
// mode, with no "don't ask again".
var askMeta = map[string]any{"anthropic/requiresUserInteraction": true}

type TokenScope struct {
	Tools   map[string]string `json:"tools"`   // tool name: off, ask or on
	Folders []string          `json:"folders"` // empty: every folder the account can see
	API     bool              `json:"api"`     // also accepted by the REST API
}

// fullScope offers every tool, asking before each call, in every folder, on the API
// too: the default for a new token, and what tokens made before scopes existed get.
func fullScope() TokenScope {
	sc := TokenScope{Tools: map[string]string{}, Folders: []string{}, API: true}
	for _, t := range mcpTools {
		sc.Tools[t.Name] = toolAsk
	}
	return sc
}

func parseScope(s string) TokenScope {
	if s == "" {
		return fullScope()
	}
	var sc TokenScope
	if json.Unmarshal([]byte(s), &sc) != nil {
		sc = TokenScope{} // unreadable: the token can do nothing
	}
	if sc.Tools == nil {
		sc.Tools = map[string]string{}
	}
	if sc.Folders == nil {
		sc.Folders = []string{}
	}
	return sc
}

// mode is how the scope offers t to u, a user already narrowed by this scope.
func (sc TokenScope) mode(u *User, t mcpTool) string {
	if t.group == groupAdmin && !u.Admin {
		return toolOff
	}
	if m := sc.Tools[t.Name]; m == toolAsk || m == toolOn {
		return m
	}
	return toolOff
}

// groups reports which kinds of tool the scope turns on.
func (sc TokenScope) groups() map[string]bool {
	on := map[string]bool{}
	for _, t := range mcpTools {
		if m := sc.Tools[t.Name]; m == toolAsk || m == toolOn {
			on[t.group] = true
		}
	}
	return on
}

// tokenLimit narrows what a user can do through one token. A nil limit allows everything.
type tokenLimit struct {
	folders            []string // empty: everywhere the user can go
	read, share, write bool
}

func (l *tokenLimit) allows(p perm) bool {
	if l == nil {
		return true
	}
	switch p {
	case permWrite:
		return l.write
	case permShare:
		return l.share
	}
	return l.read
}

// covers reports whether rel is inside one of the token's folders.
func (l *tokenLimit) covers(rel string) bool {
	if l == nil || len(l.folders) == 0 {
		return true
	}
	for _, f := range l.folders {
		if rel == f || strings.HasPrefix(rel, f+"/") {
			return true
		}
	}
	return false
}

func (l *tokenLimit) folderList() []string {
	if l == nil {
		return nil
	}
	return l.folders
}

// foldersCond is an SQL condition on col true inside the token's folders ("" if unlimited).
func (l *tokenLimit) foldersCond(col string) (string, []any) {
	if l == nil || len(l.folders) == 0 {
		return "", nil
	}
	var ors []string
	var args []any
	for _, f := range l.folders {
		c, a := insidePath(col, f)
		ors = append(ors, c)
		args = append(args, a...)
	}
	return "(" + strings.Join(ors, " OR ") + ")", args
}

// narrow returns u as a token with scope sc sees them.
func (a *App) narrow(u *User, sc TokenScope) *User {
	g := sc.groups()
	n := *u
	if u.Admin && !g[groupAdmin] {
		// no admin powers: act as someone who may view, edit and share every library
		n.Admin = false
		n.Grants = nil
		for _, root := range a.idx.enabledRoots() {
			n.Grants = append(n.Grants, Grant{Path: root, Read: true, Write: true, Share: true})
		}
	}
	n.lim = &tokenLimit{folders: sc.Folders, read: len(g) > 0, share: g[groupShare], write: g[groupEdit]}
	return &n
}

// shareInScope: may a folder-limited token see and manage link s? Only when
// everything the link opens lies inside the token's folders.
func (a *App) shareInScope(u *User, s Share) bool {
	if u.lim == nil || len(u.lim.folders) == 0 {
		return true
	}
	switch s.Mode {
	case "link":
		return u.lim.covers(s.Path)
	case "collection":
		for _, p := range a.itemPaths(s.ID) {
			if !u.lim.covers(p) {
				return false
			}
		}
		return true
	}
	return false // a drop opens no library folder
}

// cleanScope checks a scope sent by u's browser and normalizes its folders.
func (a *App) cleanScope(u *User, in TokenScope) (TokenScope, error) {
	out := TokenScope{Tools: map[string]string{}, Folders: []string{}, API: in.API}
	known := map[string]bool{}
	for _, t := range mcpTools {
		known[t.Name] = true
	}
	for name, m := range in.Tools {
		switch {
		case !known[name]:
			return out, fmt.Errorf("there is no tool called %q", name)
		case m == toolAsk || m == toolOn:
			out.Tools[name] = m
		case m != toolOff && m != "":
			return out, errors.New("each tool is off, ask or on")
		}
	}
	if len(in.Folders) > 100 {
		return out, errors.New("100 folders at most")
	}
	seen := map[string]bool{}
	for _, f := range in.Folders {
		if strings.TrimSpace(f) == "" {
			continue
		}
		_, rel, err := a.resolve(f)
		if err != nil || rel == "" || !u.canReach(rel, permRead) {
			return out, fmt.Errorf("%q isn't a folder you can see", f)
		}
		if !seen[rel] {
			seen[rel] = true
			out.Folders = append(out.Folders, rel)
		}
	}
	return out, nil
}

// ---- token API (browser sessions only: a token can't change tokens) ----

type tokenView struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	Created  int64      `json:"created"`
	LastUsed int64      `json:"last_used"`
	LastIP   string     `json:"last_ip"`
	Scope    TokenScope `json:"scope"`
}

func (a *App) apiTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.db.Query(`SELECT id,name,created,last_used,last_ip,scope FROM tokens WHERE user_id=? ORDER BY id DESC`, userOf(r).ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []tokenView{}
	for rows.Next() {
		var t tokenView
		var sc string
		_ = rows.Scan(&t.ID, &t.Name, &t.Created, &t.LastUsed, &t.LastIP, &sc)
		t.Scope = parseScope(sc)
		out = append(out, t)
	}
	writeJSON(w, out)
}

// apiTokenTools lists the MCP tools a token of this account can offer, for the settings page.
func (a *App) apiTokenTools(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	type tool struct {
		Name        string `json:"name"`
		Group       string `json:"group"`
		Description string `json:"description"`
	}
	out := []tool{}
	for _, t := range mcpTools {
		if t.group != groupAdmin || u.Admin {
			out = append(out, tool{t.Name, t.group, t.Description})
		}
	}
	writeJSON(w, out)
}

func (a *App) apiNewToken(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var in struct {
		Name  string
		Scope *TokenScope
	}
	_ = readJSON(r, &in)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "token"
	}
	sc := fullScope()
	if in.Scope != nil {
		var err error
		if sc, err = a.cleanScope(u, *in.Scope); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
	}
	b, _ := json.Marshal(sc)
	tok := "brw_" + randToken(24)
	if _, err := a.st.exec(`INSERT INTO tokens(name,hash,created,user_id,scope) VALUES(?,?,?,?,?)`, trunc(name, 80), sha(tok), now(), u.ID, string(b)); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"token": tok, "name": name, "mcp_url": a.baseURL(r) + "/mcp", "scope": sc})
}

func (a *App) apiPatchToken(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var in struct {
		Name  *string
		Scope *TokenScope
	}
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	var id int64
	if err := a.st.db.QueryRow(`SELECT id FROM tokens WHERE id=? AND user_id=?`, r.PathValue("id"), u.ID).Scan(&id); err != nil {
		httpErr(w, 404, "no such token")
		return
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			httpErr(w, 400, "give the token a name")
			return
		}
		_, _ = a.st.exec(`UPDATE tokens SET name=? WHERE id=?`, trunc(name, 80), id)
	}
	if in.Scope != nil {
		sc, err := a.cleanScope(u, *in.Scope)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		b, _ := json.Marshal(sc)
		_, _ = a.st.exec(`UPDATE tokens SET scope=? WHERE id=?`, string(b), id)
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) apiDelToken(w http.ResponseWriter, r *http.Request) {
	_, _ = a.st.exec(`DELETE FROM tokens WHERE id=? AND user_id=?`, r.PathValue("id"), userOf(r).ID)
	writeJSON(w, map[string]bool{"ok": true})
}
