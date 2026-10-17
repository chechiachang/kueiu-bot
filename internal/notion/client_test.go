package notion

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSavedViewSorts(t *testing.T) {
	for _, tc := range []struct {
		name, parent, direction string
		wantPatch               bool
	}{
		{"descending", "database", "descending", true},
		{"ascending", "database", "ascending", true},
		{"outside database", "other", "descending", false},
		{"invalid direction", "database", "newest", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patched := false
			c := NewClient("token", "database")
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/views/view" || r.Header.Get("Notion-Version") != "2026-03-11" {
					t.Fatalf("unexpected request: %s %v", r.URL, r.Header)
				}
				body := `{"id":"view","parent":{"database_id":"` + tc.parent + `"}}`
				if r.Method == http.MethodPatch {
					patched = true
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					sorts, _ := payload["sorts"].([]any)
					if len(payload) != 1 || len(sorts) != 1 {
						t.Fatalf("unexpected patch: %v", payload)
					}
					sort := sorts[0].(map[string]any)
					if sort["property"] != "Date" || sort["direction"] != tc.direction {
						t.Fatalf("unexpected sort: %v", sort)
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			_, err := c.Call(context.Background(), "update_database_view_sorts", map[string]any{"view_id": "view", "sorts": []any{map[string]any{"property": "Date", "direction": tc.direction}}})
			if patched != tc.wantPatch || (err == nil) != tc.wantPatch {
				t.Fatalf("patched=%v err=%v", patched, err)
			}
		})
	}
}

func TestListDatabaseViews(t *testing.T) {
	c := NewClient("token", "database")
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/v1/views" || q.Get("database_id") != "database" || q.Get("start_cursor") != "next" || q.Get("page_size") != "10" {
			t.Fatalf("unexpected request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[],"has_more":false}`))}, nil
	})
	if _, err := c.Call(context.Background(), "list_database_views", map[string]any{"start_cursor": "next", "page_size": 10}); err != nil {
		t.Fatal(err)
	}
}

func TestAddSelectProperty(t *testing.T) {
	for _, tc := range []struct {
		name, properties     string
		wantPatch, wantError bool
	}{
		{"new column", `{}`, true, false},
		{"preserve options", `{"Note type":{"type":"select","select":{"options":[{"id":"old","name":"other"}]}}}`, true, false},
		{"already exists", `{"Note type":{"type":"select","select":{"options":[{"id":"a","name":"instruction"},{"id":"b","name":"record"}]}}}`, false, false},
		{"wrong type", `{"Note type":{"type":"rich_text"}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patched := false
			reads := 0
			c := NewClient("token", "database")
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/databases/database" || r.Header.Get("Notion-Version") != "2022-06-28" {
					t.Fatalf("unexpected request: %s", r.URL)
				}
				if r.Method == http.MethodPatch {
					patched = true
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					props := body["properties"].(map[string]any)
					if len(body) != 1 || len(props) != 1 {
						t.Fatalf("unexpected body: %v", body)
					}
					opts := props["Note type"].(map[string]any)["select"].(map[string]any)["options"].([]any)
					offset := 0
					if tc.name == "preserve options" {
						if opts[0].(map[string]any)["id"] != "old" {
							t.Fatal("lost existing option")
						}
						offset = 1
					}
					if len(opts) != offset+2 || opts[offset].(map[string]any)["name"] != "instruction" || opts[offset+1].(map[string]any)["name"] != "record" {
						t.Fatalf("unexpected options: %v", opts)
					}
				} else {
					reads++
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"properties":` + tc.properties + `}`))}, nil
			})
			_, err := c.Call(context.Background(), "add_database_select_property", map[string]any{"name": "Note type", "options": []any{"instruction", "record"}})
			if patched != tc.wantPatch || (err != nil) != tc.wantError {
				t.Fatalf("patched=%v err=%v", patched, err)
			}
			if !tc.wantError && reads != 2 {
				t.Fatalf("expected schema readback, got %d reads", reads)
			}
		})
	}
}

func TestUpdateSelectOptionColors(t *testing.T) {
	for _, tc := range []struct {
		name, option, color string
		status              int
		wantError           bool
	}{
		{"recolor", "instruction", "blue", 200, false},
		{"unknown option", "missing", "blue", 200, true},
		{"invalid color", "instruction", "rainbow", 200, true},
		{"API failure", "instruction", "blue", 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient("token", "database")
			patched, reads := false, 0
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/v1/databases/database" {
					t.Fatalf("wrong database: %s", r.URL)
				}
				status := 200
				if r.Method == http.MethodPatch {
					patched = true
					status = tc.status
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					props := body["properties"].(map[string]any)
					if len(props) != 1 {
						t.Fatal("changed unrelated properties")
					}
					opts := props["prop"].(map[string]any)["select"].(map[string]any)["options"].([]any)
					if len(opts) != 3 {
						t.Fatal("lost options")
					}
					for i, id := range []string{"a", "b", "c"} {
						opt := opts[i].(map[string]any)
						if opt["id"] != id {
							t.Fatal("changed option identity")
						}
						if i == 0 && opt["color"] != "blue" {
							t.Fatal("color not set")
						}
						if i > 0 && len(opt) != 1 {
							t.Fatal("changed unrelated color")
						}
					}
				} else {
					reads++
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"properties":{"Note type":{"id":"prop","type":"select","select":{"options":[{"id":"a","name":"instruction","color":"blue"},{"id":"b","name":"record","color":"green"},{"id":"c","name":"other","color":"red"}]}}}}`))}, nil
			})
			_, err := c.Call(context.Background(), "update_database_select_option_colors", map[string]any{"name": "Note type", "options": []any{map[string]any{"name": tc.option, "color": tc.color}}})
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v", err)
			}
			if !tc.wantError && (!patched || reads != 2) {
				t.Fatal("expected patch and schema verification")
			}
			if (tc.name == "unknown option" || tc.name == "invalid color") && patched {
				t.Fatal("invalid update sent")
			}
		})
	}
}
