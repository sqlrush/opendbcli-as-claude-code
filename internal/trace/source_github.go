/*-------------------------------------------------------------------------
 *
 * source_github.go
 *	  GitHubLookup searches a GitHub repository for hot function
 *	  definitions using the GitHub Code Search API. No local clone
 *	  needed.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/trace/source_github.go
 *
 *-------------------------------------------------------------------------
 */
package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHubLookup searches a GitHub repository for hot function definitions
// using the GitHub Code Search API. No local clone needed.
type GitHubLookup struct {
	Repo   string // "owner/repo", e.g. "mysql/mysql-server"
	Branch string // branch name, default "main"
	Token  string // optional GitHub API token (increases rate limit)
}

// Grep finds function definitions via GitHub Code Search API.
// Returns only functions that were actually found. If Repo is empty, returns nil.
func (g *GitHubLookup) Grep(funcs []HotFunc) ([]FuncSource, error) {
	if g.Repo == "" {
		return nil, nil
	}
	branch := g.Branch
	if branch == "" {
		branch = "main"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var results []FuncSource
	for _, f := range funcs {
		src, err := g.searchFunc(ctx, f.Name, branch)
		if err != nil {
			// Rate limit or network error — stop searching, return what we have.
			break
		}
		if src != nil {
			results = append(results, *src)
		}
	}
	return results, nil
}

// searchFunc searches GitHub for a single function definition.
func (g *GitHubLookup) searchFunc(ctx context.Context, funcName string, branch string) (*FuncSource, error) {
	// Strip class prefix for search: "ha_innodb::write_row" → "write_row"
	searchName := funcName
	if idx := strings.LastIndex(funcName, "::"); idx >= 0 {
		searchName = funcName[idx+2:]
	}

	// Build search query: function name + repo + C/C++ language filter.
	query := fmt.Sprintf("%s repo:%s language:c language:cpp", searchName, g.Repo)
	apiURL := fmt.Sprintf("https://api.github.com/search/code?q=%s&per_page=3",
		url.QueryEscape(query))

	body, err := g.apiGet(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var searchResult codeSearchResult
	if err := json.Unmarshal(body, &searchResult); err != nil {
		return nil, fmt.Errorf("parse search result: %w", err)
	}

	if len(searchResult.Items) == 0 {
		return nil, nil
	}

	// Pick the best match: prefer .cc/.cpp files in non-test directories.
	item := pickBestMatch(searchResult.Items, funcName)

	// Fetch file content to extract snippet.
	snippet, line, err := g.fetchSnippet(ctx, item.HTMLURL, item.Path, searchName, branch)
	if err != nil {
		// Can't fetch content — return what we have without snippet.
		return &FuncSource{
			FuncName: funcName,
			FilePath: item.Path,
		}, nil
	}

	return &FuncSource{
		FuncName: funcName,
		FilePath: item.Path,
		Line:     line,
		Snippet:  snippet,
	}, nil
}

// fetchSnippet fetches raw file content from GitHub and extracts the function snippet.
func (g *GitHubLookup) fetchSnippet(ctx context.Context, htmlURL, path, shortName, branch string) (string, int, error) {
	// Raw content URL.
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", g.Repo, branch, path)

	body, err := g.apiGet(ctx, rawURL)
	if err != nil {
		return "", 0, err
	}

	// Search for function definition in the content.
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		if isFuncDef(line, shortName) {
			// Collect up to 50 lines.
			end := i + 50
			if end > len(lines) {
				end = len(lines)
			}
			var sb strings.Builder
			for j := i; j < end; j++ {
				sb.WriteString(lines[j])
				sb.WriteByte('\n')
				trimmed := strings.TrimSpace(lines[j])
				if j > i && trimmed == "}" {
					break
				}
			}
			return sb.String(), i + 1, nil
		}
	}
	return "", 0, fmt.Errorf("function %q not found in %s", shortName, path)
}

// apiGet performs an HTTP GET request to the GitHub API.
func (g *GitHubLookup) apiGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "opendb-trace")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return nil, fmt.Errorf("GitHub API 频率限制，配置 trace.source.token 可提高限额")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return body, nil
}

// codeSearchResult is the GitHub Code Search API response.
type codeSearchResult struct {
	Items []codeSearchItem `json:"items"`
}

type codeSearchItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	HTMLURL string `json:"html_url"`
}

// pickBestMatch selects the best file from search results.
// Prefers .cc/.cpp files in non-test paths.
func pickBestMatch(items []codeSearchItem, funcName string) codeSearchItem {
	for _, item := range items {
		lower := strings.ToLower(item.Path)
		isTest := strings.Contains(lower, "/test/") || strings.Contains(lower, "/unittest/") ||
			strings.HasPrefix(lower, "test/") || strings.HasPrefix(lower, "unittest/")
		isSource := strings.HasSuffix(lower, ".cc") || strings.HasSuffix(lower, ".cpp") || strings.HasSuffix(lower, ".c")
		if isSource && !isTest {
			return item
		}
	}
	return items[0]
}
