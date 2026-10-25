package main

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every link on a folder link's pages works, however deep, including names with
// spaces and other characters that need escaping, and each row's download button.
func TestShareLinksReachEveryLevel(t *testing.T) {
	e := setup(t)
	base := filepath.Join(e.data, "PROJECTS", "Claude Projects", "Quartermaster")
	files := map[string]string{
		"qm.exe": "exe",
		"quartermaster-models/models & voices.json":                "{}",
		"quartermaster-models/text-to-speech/voice one.gguf":       "gguf",
		"quartermaster-models/text-to-speech/deeper #2/notes.txt": "deep",
	}
	for rel, body := range files {
		p := filepath.Join(base, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(body), 0o644)
	}
	var s Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "PROJECTS/Claude Projects/Quartermaster"}, 200), &s)
	get := func(u string) (int, string) {
		r, err := http.Get(e.srv.URL + u)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	href := regexp.MustCompile(`(?:href|src)="(/s/[^"]+)"`)
	seen := map[string]bool{}
	downloads := map[string]string{}
	queue := []string{"/s/" + s.ID}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if seen[u] {
			continue
		}
		seen[u] = true
		code, body := get(u)
		if code != 200 {
			t.Fatalf("%s -> %d", u, code)
		}
		if strings.Contains(u, "/dl?") {
			downloads[u] = body
			continue
		}
		if strings.Contains(u, "/zip") || strings.Contains(u, "/raw") || strings.Contains(u, "/thumb") {
			continue
		}
		for _, m := range href.FindAllStringSubmatch(body, -1) {
			queue = append(queue, html.UnescapeString(m[1]))
		}
	}
	// every file got a working download button, deepest level included
	got := map[string]bool{}
	for _, body := range downloads {
		got[body] = true
	}
	for rel, body := range files {
		if !got[body] {
			t.Fatalf("no working download for %s (visited %d links)", rel, len(seen))
		}
	}
}
