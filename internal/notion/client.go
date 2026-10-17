package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiBase = "https://api.notion.com/v1"

type Client struct {
	token      string
	databaseID string
	http       *http.Client
}

func NewClient(token, databaseID string) *Client {
	return &Client{
		token:      token,
		databaseID: databaseID,
		http:       &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Call(ctx context.Context, name string, args map[string]any) (any, error) {
	switch name {
	case "inspect_database_schema":
		var result map[string]any
		err := c.request(ctx, http.MethodGet, "/databases/"+url.PathEscape(c.databaseID), nil, &result)
		return result, err
	case "update_database_select_option_colors":
		return c.updateSelectOptionColors(ctx, args)
	case "add_database_select_property":
		return c.addSelectProperty(ctx, args)
	case "query_database":
		body := map[string]any{}
		for _, key := range []string{"filter", "sorts", "start_cursor"} {
			if value, ok := args[key]; ok {
				body[key] = value
			}
		}
		body["page_size"] = pageSize(args)
		var result map[string]any
		err := c.request(ctx, http.MethodPost, "/databases/"+url.PathEscape(c.databaseID)+"/query", body, &result)
		return result, err
	case "list_database_views":
		query := url.Values{"database_id": {c.databaseID}, "page_size": {fmt.Sprint(pageSize(args))}}
		if cursor := stringArg(args, "start_cursor"); cursor != "" {
			query.Set("start_cursor", cursor)
		}
		var result map[string]any
		err := c.request(ctx, http.MethodGet, "/views?"+query.Encode(), nil, &result)
		return result, err
	case "get_database_view", "update_database_view_sorts":
		viewID := stringArg(args, "view_id")
		if viewID == "" {
			return nil, errors.New("view_id is required")
		}
		path := "/views/" + url.PathEscape(viewID)
		var view map[string]any
		if err := c.request(ctx, http.MethodGet, path, nil, &view); err != nil {
			return nil, err
		}
		parent, _ := view["parent"].(map[string]any)
		parentID, _ := parent["database_id"].(string)
		if !sameID(parentID, c.databaseID) {
			return nil, errors.New("view is not in the configured database")
		}
		if name == "get_database_view" {
			return view, nil
		}
		sorts, ok := args["sorts"].([]any)
		if !ok || len(sorts) == 0 {
			return nil, errors.New("a nonempty sorts array is required")
		}
		for _, value := range sorts {
			sort, ok := value.(map[string]any)
			if !ok || stringArg(sort, "property") == "" {
				return nil, errors.New("each sort requires a property")
			}
			direction := stringArg(sort, "direction")
			if direction != "ascending" && direction != "descending" {
				return nil, errors.New("sort direction must be ascending or descending")
			}
			if len(sort) != 2 {
				return nil, errors.New("sort supports only property and direction")
			}
		}
		var result map[string]any
		err := c.request(ctx, http.MethodPatch, path, map[string]any{"sorts": sorts}, &result)
		return result, err
	case "get_page":
		pageID := stringArg(args, "page_id")
		var page map[string]any
		if err := c.request(ctx, http.MethodGet, "/pages/"+url.PathEscape(pageID), nil, &page); err != nil {
			return nil, err
		}
		blocks, err := c.pageBlocks(ctx, pageID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": page, "blocks": blocks}, nil
	case "search_pages":
		body := map[string]any{
			"query":     stringArg(args, "query"),
			"filter":    map[string]any{"property": "object", "value": "page"},
			"page_size": pageSize(args),
		}
		if cursor, ok := args["start_cursor"]; ok {
			body["start_cursor"] = cursor
		}
		var result map[string]any
		err := c.request(ctx, http.MethodPost, "/search", body, &result)
		return result, err
	case "create_page":
		properties, ok := args["properties"].(map[string]any)
		if !ok || len(properties) == 0 {
			return nil, errors.New("properties are required")
		}
		body := map[string]any{
			"parent":     map[string]any{"database_id": c.databaseID},
			"properties": properties,
		}
		if content := stringArg(args, "content"); content != "" {
			body["children"] = paragraphs(content)
		}
		var result map[string]any
		err := c.request(ctx, http.MethodPost, "/pages", body, &result)
		return pageReference(result), err
	case "update_page_properties":
		pageID := stringArg(args, "page_id")
		properties, ok := args["properties"].(map[string]any)
		if !ok || len(properties) == 0 {
			return nil, errors.New("properties are required")
		}
		if err := c.ensureDatabasePage(ctx, pageID); err != nil {
			return nil, err
		}
		var result map[string]any
		err := c.request(ctx, http.MethodPatch, "/pages/"+url.PathEscape(pageID), map[string]any{"properties": properties}, &result)
		return pageReference(result), err
	case "append_page_content":
		pageID := stringArg(args, "page_id")
		if err := c.ensureDatabasePage(ctx, pageID); err != nil {
			return nil, err
		}
		children, err := contentBlocks(args)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		err = c.request(ctx, http.MethodPatch, "/blocks/"+url.PathEscape(pageID)+"/children", map[string]any{"children": children}, &result)
		if err != nil {
			return nil, err
		}
		page, err := c.pageReference(ctx, pageID)
		return map[string]any{"page": page, "blocks": result["results"]}, err
	case "update_page_content_block":
		blockID := stringArg(args, "block_id")
		pageID, err := c.databaseBlockPageID(ctx, blockID)
		if err != nil {
			return nil, err
		}
		block, ok := args["block"].(map[string]any)
		if !ok || len(block) == 0 {
			return nil, errors.New("block is required")
		}
		var result map[string]any
		err = c.request(ctx, http.MethodPatch, "/blocks/"+url.PathEscape(blockID), block, &result)
		if err != nil {
			return nil, err
		}
		page, err := c.pageReference(ctx, pageID)
		return map[string]any{"page": page, "block": result}, err
	default:
		return nil, errors.New("unknown Notion operation")
	}
}

var selectColors = []string{"default", "gray", "brown", "orange", "yellow", "green", "blue", "purple", "pink", "red"}

func (c *Client) updateSelectOptionColors(ctx context.Context, args map[string]any) (any, error) {
	name := stringArg(args, "name")
	updates, ok := args["options"].([]any)
	if name == "" || !ok || len(updates) == 0 {
		return nil, errors.New("name and a nonempty options array are required")
	}
	colors := map[string]string{}
	for _, value := range updates {
		option, _ := value.(map[string]any)
		optionName, color := stringArg(option, "name"), stringArg(option, "color")
		valid := false
		for _, allowed := range selectColors {
			if color == allowed {
				valid = true
			}
		}
		key := strings.ToLower(optionName)
		if optionName == "" || !valid || colors[key] != "" {
			return nil, errors.New("each option requires a unique name and a valid Notion color")
		}
		colors[key] = color
	}
	path := "/databases/" + url.PathEscape(c.databaseID)
	var database map[string]any
	if err := c.request(ctx, http.MethodGet, path, nil, &database); err != nil {
		return nil, err
	}
	properties, _ := database["properties"].(map[string]any)
	property, _ := properties[name].(map[string]any)
	if property["type"] != "select" {
		return nil, errors.New("property must be an existing Select column")
	}
	config, _ := property["select"].(map[string]any)
	existing, ok := config["options"].([]any)
	if !ok {
		return nil, errors.New("existing Select options are missing")
	}
	options := make([]any, 0, len(existing))
	for _, value := range existing {
		option, _ := value.(map[string]any)
		id := stringArg(option, "id")
		if id == "" {
			return nil, errors.New("existing option ID is missing")
		}
		entry := map[string]any{"id": id}
		key := strings.ToLower(stringArg(option, "name"))
		if color, found := colors[key]; found {
			entry["color"] = color
			delete(colors, key)
		}
		options = append(options, entry)
	}
	if len(colors) != 0 {
		return nil, errors.New("requested option does not exist in the Select column")
	}
	propertyKey := stringArg(property, "id")
	if propertyKey == "" {
		propertyKey = name
	}
	body := map[string]any{"properties": map[string]any{propertyKey: map[string]any{"select": map[string]any{"options": options}}}}
	if err := c.request(ctx, http.MethodPatch, path, body, nil); err != nil {
		return nil, err
	}
	var result map[string]any
	if err := c.request(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// addSelectProperty preserves existing options when the column already exists.
func (c *Client) addSelectProperty(ctx context.Context, args map[string]any) (any, error) {
	name := stringArg(args, "name")
	values, ok := args["options"].([]any)
	if name == "" || !ok || len(values) == 0 {
		return nil, errors.New("name and a nonempty options array are required")
	}
	var requested []string
	for _, value := range values {
		option, ok := value.(string)
		option = strings.TrimSpace(option)
		if !ok || option == "" || strings.Contains(option, ",") {
			return nil, errors.New("select options must be nonempty strings without commas")
		}
		requested = append(requested, option)
	}
	path := "/databases/" + url.PathEscape(c.databaseID)
	var database map[string]any
	if err := c.request(ctx, http.MethodGet, path, nil, &database); err != nil {
		return nil, err
	}
	properties, ok := database["properties"].(map[string]any)
	if !ok {
		return nil, errors.New("database response is missing its property schema")
	}
	options := []any{}
	seen := map[string]bool{}
	if existing, exists := properties[name]; exists {
		property, _ := existing.(map[string]any)
		if property["type"] != "select" {
			return nil, errors.New("existing property is not a select; cannot change its type")
		}
		selectConfig, _ := property["select"].(map[string]any)
		existingOptions, ok := selectConfig["options"].([]any)
		if !ok {
			return nil, errors.New("existing select property is missing its options")
		}
		for _, value := range existingOptions {
			option, _ := value.(map[string]any)
			optionName := stringArg(option, "name")
			id := stringArg(option, "id")
			if optionName == "" || id == "" {
				return nil, errors.New("existing select option is missing its name or ID")
			}
			options = append(options, map[string]any{"id": id})
			seen[strings.ToLower(optionName)] = true
		}
	}
	changed := false
	for _, option := range requested {
		key := strings.ToLower(option)
		if !seen[key] {
			options = append(options, map[string]any{"name": option})
			seen[key] = true
			changed = true
		}
	}
	if changed {
		body := map[string]any{"properties": map[string]any{
			name: map[string]any{"select": map[string]any{"options": options}},
		}}
		if err := c.request(ctx, http.MethodPatch, path, body, nil); err != nil {
			return nil, err
		}
	}
	var result map[string]any
	if err := c.request(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) pageBlocks(ctx context.Context, pageID string) ([]any, error) {
	var blocks []any
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		path := "/blocks/" + url.PathEscape(pageID) + "/children?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + url.QueryEscape(cursor)
		}
		var result struct {
			Results    []any  `json:"results"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if err := c.request(ctx, http.MethodGet, path, nil, &result); err != nil {
			return nil, err
		}
		blocks = append(blocks, result.Results...)
		if !result.HasMore || result.NextCursor == "" {
			return blocks, nil
		}
		cursor = result.NextCursor
	}
	return blocks, nil
}

func (c *Client) ensureDatabasePage(ctx context.Context, pageID string) error {
	var page map[string]any
	if err := c.request(ctx, http.MethodGet, "/pages/"+url.PathEscape(pageID), nil, &page); err != nil {
		return err
	}
	parent, _ := page["parent"].(map[string]any)
	if parentID, _ := parent["database_id"].(string); sameID(parentID, c.databaseID) {
		return nil
	}
	if parentID, _ := parent["data_source_id"].(string); sameID(parentID, c.databaseID) {
		return nil
	}
	return errors.New("page is not in the configured database")
}

func (c *Client) databaseBlockPageID(ctx context.Context, blockID string) (string, error) {
	for depth := 0; depth < 10; depth++ {
		var block map[string]any
		if err := c.request(ctx, http.MethodGet, "/blocks/"+url.PathEscape(blockID), nil, &block); err != nil {
			return "", err
		}
		parent, _ := block["parent"].(map[string]any)
		if pageID, _ := parent["page_id"].(string); pageID != "" {
			if err := c.ensureDatabasePage(ctx, pageID); err != nil {
				return "", err
			}
			return pageID, nil
		}
		if nextBlockID, _ := parent["block_id"].(string); nextBlockID != "" {
			blockID = nextBlockID
			continue
		}
		return "", errors.New("content block is not on a configured database page")
	}
	return "", errors.New("content block nesting is too deep")
}

func (c *Client) pageReference(ctx context.Context, pageID string) (any, error) {
	var page map[string]any
	if err := c.request(ctx, http.MethodGet, "/pages/"+url.PathEscape(pageID), nil, &page); err != nil {
		return nil, err
	}
	return pageReference(page), nil
}

func sameID(left, right string) bool {
	return left != "" && strings.ReplaceAll(strings.ToLower(left), "-", "") == strings.ReplaceAll(strings.ToLower(right), "-", "")
}

func (c *Client) request(ctx context.Context, method, path string, body any, dest any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return errors.New("encode Notion request")
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, reader)
	if err != nil {
		return errors.New("create Notion request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	version := "2022-06-28"
	if strings.HasPrefix(path, "/views/") || strings.HasPrefix(path, "/views?") {
		version = "2026-03-11"
	}
	req.Header.Set("Notion-Version", version)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Notion request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Notion request failed with HTTP %d", resp.StatusCode)
	}
	if dest == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dest); err != nil {
		return errors.New("decode Notion response")
	}
	return nil
}

func pageSize(args map[string]any) int {
	switch value := args["page_size"].(type) {
	case float64:
		if value >= 1 && value <= 100 {
			return int(value)
		}
	case int:
		if value >= 1 && value <= 100 {
			return value
		}
	}
	return 100
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func paragraphs(content string) []any {
	var children []any
	for _, line := range strings.Split(content, "\n") {
		if line == "" {
			continue
		}
		children = append(children, map[string]any{
			"object": "block",
			"type":   "paragraph",
			"paragraph": map[string]any{
				"rich_text": []any{map[string]any{"type": "text", "text": map[string]any{"content": line}}},
			},
		})
	}
	return children
}

func contentBlocks(args map[string]any) ([]any, error) {
	if children, ok := args["children"].([]any); ok && len(children) > 0 {
		return children, nil
	}
	if content := stringArg(args, "content"); content != "" {
		return paragraphs(content), nil
	}
	return nil, errors.New("content or children are required")
}

func pageReference(page map[string]any) any {
	return map[string]any{"id": page["id"], "url": page["url"]}
}
