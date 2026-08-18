package toolbelt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	jsUrlRe      = regexp.MustCompile(`(?i)["'](https?://[^"'\s]+\.js(?:\?[^"'\s]*)?)["']|["']([^"'\s]+\.js(?:\?[^"'\s]*)?)["']`)
	httpUrlRe    = regexp.MustCompile(`(?i)(?:https?:)?//[a-z0-9._~:/?#@!$&'()*+,;=%-]+|["'](/[a-z0-9._~:/?#@!$&'()*+,;=%-]+)["']`)
	apiPathRe    = regexp.MustCompile(`["'](/(?:api|v1|v2|admin|user|auth|login|graphql|upload|download|file|asset|static|console|debug)[a-z0-9._~:/?#@!$&'()*+,;=%-]*)["']`)
	credentialRe = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|passwd|access[_-]?key|client[_-]?secret|aws|gcp|azure|firebase|jwt)["'\s:=]+["']?([a-z0-9._+/=~\-]{8,})["']?`)
)

// fetchJS crawls a page for its JS assets and returns their source.
func fetchJS(ctx context.Context, args map[string]interface{}) toolResult {
	target := argString(args, "url", "")
	if target == "" {
		return errResult("fetch_js: `url` required")
	}
	depth := argInt(args, "depth", 2)
	maxJS := argInt(args, "max", 30)
	client := httpClient(argBool(args, "insecure", false), 15*time.Second, 3)

	// 1) try katana; fall back to fetching the page HTML directly
	html := ""
	if out, err := safeExec(ctx, 5*time.Minute, "katana",
		"-u", target, "-d", strconv.Itoa(depth), "-jc", "-kf", "all", "-silent"); err == nil {
		html = out
	} else {
		req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; dhunter/1.0)")
		if resp, err := client.Do(req); err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			resp.Body.Close()
			html = string(b)
		}
	}

	jsURLs := extractJSURLs(target, html)
	if len(jsURLs) > maxJS {
		jsURLs = jsURLs[:maxJS]
	}

	type jsResult struct {
		URL  string `json:"url"`
		Body string `json:"body,omitempty"`
		Size int    `json:"size"`
		Err  string `json:"err,omitempty"`
	}
	results := make([]jsResult, 0, len(jsURLs))
	for _, u := range jsURLs {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; dhunter/1.0)")
		resp, err := client.Do(req)
		if err != nil {
			results = append(results, jsResult{URL: u, Err: err.Error()})
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		results = append(results, jsResult{URL: u, Body: string(b), Size: len(b)})
	}
	out, _ := json.Marshal(map[string]interface{}{"target": target, "count": len(results), "js": results})
	return textResult(string(out))
}

func extractJSURLs(base, text string) []string {
	parsed, _ := url.Parse(base)
	seen := map[string]struct{}{}
	out := []string{}
	for _, m := range jsUrlRe.FindAllStringSubmatch(text, -1) {
		u := m[1]
		if u == "" {
			u = m[2]
		}
		if !strings.HasPrefix(u, "http") && parsed != nil {
			if strings.HasPrefix(u, "/") {
				u = parsed.Scheme + "://" + parsed.Host + u
			} else {
				u = parsed.Scheme + "://" + parsed.Host + "/" + u
			}
		}
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

// jsAnalyzer extracts URLs, API paths, and credential-like strings from JS.
func jsAnalyzer(ctx context.Context, args map[string]interface{}) toolResult {
	source := argString(args, "source", "")
	if source == "" {
		return errResult("js_analyzer: `source` required")
	}
	if len(source) > 512<<10 {
		source = source[:512<<10] // bound regex work
	}

	urls := dedupNonEmpty(httpUrlRe.FindAllString(source, -1))
	apiHits := apiPathRe.FindAllStringSubmatch(source, -1)
	apiPaths := dedupNonEmpty(func() []string {
		var out []string
		for _, m := range apiHits {
			if len(m) >= 2 {
				out = append(out, strings.Trim(m[1], `"'`))
			}
		}
		return out
	}())
	creds := []map[string]string{}
	for _, m := range credentialRe.FindAllStringSubmatch(source, -1) {
		if len(m) >= 3 {
			creds = append(creds, map[string]string{"key": strings.ToLower(m[1]), "value": m[2]})
		}
	}

	out, _ := json.Marshal(map[string]interface{}{
		"url":       argString(args, "url", ""),
		"urls":      urls,
		"api_paths": apiPaths,
		"creds":     creds,
		"stats": map[string]int{
			"url_count": len(urls), "api_count": len(apiPaths), "cred_count": len(creds),
		},
	})
	return textResult(string(out))
}

// leakCreds probes common sensitive paths and reports reachable ones.
func leakCreds(ctx context.Context, args map[string]interface{}) toolResult {
	target := argString(args, "url", "")
	if target == "" {
		return errResult("leak_creds: `url` required")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return errResult("leak_creds: bad url")
	}

	paths := []string{
		".git/HEAD", ".git/config", ".env", ".env.bak", ".env.local", ".env.production",
		".DS_Store", "Thumbs.db", ".svn/entries", ".svn/wc.db",
		".htaccess", ".htpasswd", "web.config",
		"backup.zip", "backup.tar.gz", "backup.sql", "dump.sql",
		"robots.txt", "sitemap.xml", "crossdomain.xml",
		"phpinfo.php", "info.php", "test.php", "test.html",
		"admin/", "administrator/", "manage/", "backend/", "manager/",
		"api/", "api/v1/", "swagger/", "swagger.json", "swagger.yaml",
		"v1/docs", "v2/docs", "openapi.json", "openapi.yaml",
		"actuator/", "actuator/env", "actuator/health", "actuator/beans",
		"console/", "_debug/", "debug/", "trace.axd",
		"server-status", "server-info", "phpmyadmin/", "adminer.php",
		"web.config.bak", "config.php.bak", "config.bak",
	}

	client := httpClient(argBool(args, "insecure", false), 8*time.Second, 3)
	type result struct {
		Path   string `json:"path"`
		Status int    `json:"status"`
		Size   int    `json:"size"`
		URL    string `json:"url"`
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		hits []result
		sem  = make(chan struct{}, 10)
	)
	for _, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			full := strings.TrimRight(parsed.String(), "/") + "/" + p
			req, _ := http.NewRequestWithContext(ctx, "GET", full, nil)
			req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; dhunter/1.0)")
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if resp.StatusCode == 200 || resp.StatusCode == 206 {
				mu.Lock()
				hits = append(hits, result{Path: p, Status: resp.StatusCode, Size: len(body), URL: full})
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()

	out, _ := json.Marshal(map[string]interface{}{
		"target": target, "scanned": len(paths), "hits": hits,
	})
	return textResult(string(out))
}
