package toolbelt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// fofaSearch queries the FOFA asset-search API.
func fofaSearch(ctx context.Context, args map[string]interface{}) toolResult {
	q := argString(args, "query", "")
	if q == "" {
		return errResult("fofa_search: `query` required (FOFA syntax)")
	}
	email := argString(args, "email", os.Getenv("FOFA_EMAIL"))
	key := argString(args, "key", os.Getenv("FOFA_KEY"))
	if email == "" || key == "" {
		return errResult("fofa_search: FOFA_EMAIL/FOFA_KEY missing (env or args)")
	}
	size := argInt(args, "size", 100)
	if size > 10000 {
		size = 10000
	}
	form := url.Values{
		"email":   {email},
		"key":     {key},
		"qbase64": {base64.StdEncoding.EncodeToString([]byte(q))},
		"size":    {strconv.Itoa(size)},
		"fields":  {"host,ip,port,protocol,country,title,server,cert"},
		"page":    {"1"},
	}
	client := httpClient(false, 30*time.Second, 3)
	resp, err := client.PostForm("https://fofa.info/api/v1/search/all", form)
	if err != nil {
		return errResult("fofa_search: " + err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return errResult(fmt.Sprintf("fofa_search: http %d: %s", resp.StatusCode, body[:minInt(len(body), 300)]))
	}
	var parsed struct {
		Error   bool       `json:"error"`
		Msg     string     `json:"msg"`
		Results [][]string `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return errResult("fofa_search: parse error")
	}
	if parsed.Error {
		return errResult("fofa_search: " + parsed.Msg)
	}
	out, _ := json.Marshal(map[string]interface{}{
		"query":   q,
		"size":    len(parsed.Results),
		"results": parsed.Results,
	})
	return textResult(string(out))
}

// subfinderEnum enumerates subdomains via the subfinder binary.
func subfinderEnum(ctx context.Context, args map[string]interface{}) toolResult {
	domain := argString(args, "domain", "")
	if domain == "" {
		return errResult("subfinder_enum: `domain` required")
	}
	bin := argString(args, "bin", "subfinder")
	cmd := []string{"-d", domain, "-all", "-silent"}
	if threads := argInt(args, "threads", 0); threads > 0 {
		cmd = append(cmd, "-t", strconv.Itoa(threads))
	}
	out, err := safeExec(ctx, 5*time.Minute, bin, cmd...)
	if err != nil {
		return errResult("subfinder_enum: " + err.Error())
	}
	hosts := dedupNonEmpty(strings.Split(out, "\n"))
	r, _ := json.Marshal(map[string]interface{}{"domain": domain, "count": len(hosts), "subdoms": hosts})
	return textResult(string(r))
}

// assetfinderEnum does passive subdomain enumeration via assetfinder.
func assetfinderEnum(ctx context.Context, args map[string]interface{}) toolResult {
	domain := argString(args, "domain", "")
	if domain == "" {
		return errResult("assetfinder_enum: `domain` required")
	}
	bin := argString(args, "bin", "assetfinder")
	out, err := safeExec(ctx, 3*time.Minute, bin, "--subs-only", domain)
	if err != nil {
		return errResult("assetfinder_enum: " + err.Error())
	}
	hosts := dedupNonEmpty(strings.Split(out, "\n"))
	r, _ := json.Marshal(map[string]interface{}{"domain": domain, "count": len(hosts), "subdoms": hosts})
	return textResult(string(r))
}

// gauHistory pulls historical URLs via gau.
func gauHistory(ctx context.Context, args map[string]interface{}) toolResult {
	domain := argString(args, "domain", "")
	if domain == "" {
		return errResult("gau_history: `domain` required")
	}
	bin := argString(args, "bin", "gau")
	out, err := safeExec(ctx, 3*time.Minute, bin, domain)
	if err != nil {
		return errResult("gau_history: " + err.Error())
	}
	urls := dedupNonEmpty(strings.Split(out, "\n"))
	r, _ := json.Marshal(map[string]interface{}{"domain": domain, "count": len(urls), "urls": urls})
	return textResult(string(r))
}

// waybackHistory pulls historical URLs via waybackurls.
func waybackHistory(ctx context.Context, args map[string]interface{}) toolResult {
	domain := argString(args, "domain", "")
	if domain == "" {
		return errResult("wayback_history: `domain` required")
	}
	bin := argString(args, "bin", "waybackurls")
	out, err := safeExec(ctx, 3*time.Minute, bin, domain)
	if err != nil {
		return errResult("wayback_history: " + err.Error())
	}
	urls := dedupNonEmpty(strings.Split(out, "\n"))
	r, _ := json.Marshal(map[string]interface{}{"domain": domain, "count": len(urls), "urls": urls})
	return textResult(string(r))
}

// katanaCrawl crawls a site for links via katana.
func katanaCrawl(ctx context.Context, args map[string]interface{}) toolResult {
	target := argString(args, "url", "")
	if target == "" {
		return errResult("katana_crawl: `url` required")
	}
	bin := argString(args, "bin", "katana")
	depth := strconv.Itoa(argInt(args, "depth", 2))
	out, err := safeExec(ctx, 8*time.Minute, bin, "-u", target, "-d", depth, "-silent", "-kf", "all")
	if err != nil {
		return errResult("katana_crawl: " + err.Error())
	}
	urls := dedupNonEmpty(strings.Split(out, "\n"))
	r, _ := json.Marshal(map[string]interface{}{"target": target, "count": len(urls), "urls": urls})
	return textResult(string(r))
}

// httpxProbe fingerprints a list of targets via httpx.
func httpxProbe(ctx context.Context, args map[string]interface{}) toolResult {
	targets := argStringSlice(args, "targets")
	if len(targets) == 0 {
		if t := argString(args, "target", ""); t != "" {
			targets = []string{t}
		}
	}
	if len(targets) == 0 {
		return errResult("httpx_probe: `target` or `targets` required")
	}
	f, err := os.CreateTemp("", "dhunter-httpx-*.txt")
	if err != nil {
		return errResult("httpx_probe: " + err.Error())
	}
	defer os.Remove(f.Name())
	for _, t := range targets {
		fmt.Fprintln(f, t)
	}
	f.Close()

	bin := argString(args, "bin", "httpx")
	cmd := []string{"-l", f.Name(), "-silent", "-no-color", "-status-code", "-title", "-tech-detect", "-content-length", "-web-server", "-follow-redirects"}
	if timeout := argInt(args, "timeout", 0); timeout > 0 {
		cmd = append(cmd, "-timeout", strconv.Itoa(timeout))
	}
	out, err := safeExec(ctx, 10*time.Minute, bin, cmd...)
	if err != nil {
		return errResult("httpx_probe: " + err.Error())
	}
	return textResult(out)
}

// --- search engines & ICP ----------------------------------------------

var (
	// NOTE: the title capture group must be a REAL capture group `(...)`,
	// not `(?:...)` — searchEngine reads m[2] for the title. The old
	// non-capturing form made m have length 2 and panicked on m[2]
	// ("index out of range") whenever a link matched.
	searchLinkRe = regexp.MustCompile(`<a[^>]+href="(https?://[^"]+)"[^>]*>([\s\S]*?)</a>`)
	tagStripRe   = regexp.MustCompile(`<[^>]+>`)
	icpNumRe     = regexp.MustCompile(`(?:备案号|icp)[:：\s]*([京津沪渝冀豫云辽黑湘皖鲁新苏浙赣鄂桂甘晋蒙陕吉闽贵粤青藏川宁琼][A-Z]\w{5,7}号)`)
	icpOwnerRe   = regexp.MustCompile(`(?:主办单位|主办方|主办人)[:：\s]*([^<\n]+?)(?:<|$)`)
)

func searchEngine(ctx context.Context, base, q, ua string, num int) ([]map[string]string, error) {
	client := httpClient(false, 15*time.Second, 3)
	req, _ := http.NewRequestWithContext(ctx, "GET", base+url.QueryEscape(q), nil)
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	matches := searchLinkRe.FindAllStringSubmatch(string(body), num*3)
	seen := map[string]struct{}{}
	out := make([]map[string]string, 0, num)
	for _, m := range matches {
		// Defensive: never index past the submatch slice, no matter how
		// the regex evolves (this exact panic happened before).
		if len(m) < 3 {
			continue
		}
		href := m[1]
		if strings.Contains(href, "baidu.com") || strings.Contains(href, "bing.com") || strings.Contains(href, "microsoft.com") {
			continue
		}
		if _, ok := seen[href]; ok {
			continue
		}
		seen[href] = struct{}{}
		title := strings.TrimSpace(tagStripRe.ReplaceAllString(m[2], ""))
		if title == "" {
			title = href
		}
		out = append(out, map[string]string{"title": title, "url": href})
		if len(out) >= num {
			break
		}
	}
	return out, nil
}

func baiduSearch(ctx context.Context, args map[string]interface{}) toolResult {
	q := argString(args, "query", "")
	if q == "" {
		return errResult("baidu_search: `query` required")
	}
	results, err := searchEngine(ctx, "https://www.baidu.com/s?wd=", q,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
		argInt(args, "num", 10))
	if err != nil {
		return errResult("baidu_search: " + err.Error())
	}
	r, _ := json.Marshal(map[string]interface{}{"query": q, "count": len(results), "results": results})
	return textResult(string(r))
}

func bingSearch(ctx context.Context, args map[string]interface{}) toolResult {
	q := argString(args, "query", "")
	if q == "" {
		return errResult("bing_search: `query` required")
	}
	results, err := searchEngine(ctx, "https://cn.bing.com/search?q=", q,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36",
		argInt(args, "num", 10))
	if err != nil {
		return errResult("bing_search: " + err.Error())
	}
	r, _ := json.Marshal(map[string]interface{}{"query": q, "count": len(results), "results": results})
	return textResult(string(r))
}

var domainRe = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

func icpLookup(ctx context.Context, args map[string]interface{}) toolResult {
	keyword := argString(args, "keyword", "")
	if keyword == "" {
		return errResult("icp_lookup: `keyword` required (company name or domain)")
	}
	client := httpClient(false, 15*time.Second, 3)
	if domainRe.MatchString(keyword) {
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://icp.aizhan.com/"+keyword, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/127.0.0.0")
		resp, err := client.Do(req)
		if err != nil {
			return errResult("icp_lookup: " + err.Error())
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		icpNum, owner := "", ""
		if m := icpNumRe.FindStringSubmatch(string(body)); len(m) >= 2 {
			icpNum = m[1]
		}
		if m := icpOwnerRe.FindStringSubmatch(string(body)); len(m) >= 2 {
			owner = strings.TrimSpace(m[1])
		}
		r, _ := json.Marshal(map[string]interface{}{"domain": keyword, "icp_num": icpNum, "owner": owner, "source": "aizhan"})
		return textResult(string(r))
	}
	// company name: search Baidu for likely ICP pages
	results, err := searchEngine(ctx, "https://www.baidu.com/s?wd=", keyword+" 备案 域名",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/127.0.0.0", 10)
	if err != nil {
		return errResult("icp_lookup: " + err.Error())
	}
	r, _ := json.Marshal(map[string]interface{}{"keyword": keyword, "results": results, "hint": "use keyword=<domain> for exact ICP lookup"})
	return textResult(string(r))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
