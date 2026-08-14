package toolbelt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// httpRequest is the agent's swiss-army knife: arbitrary HTTP probing with
// full control over method, headers, cookies, and body.
func httpRequest(ctx context.Context, args map[string]interface{}) toolResult {
	method := strings.ToUpper(argString(args, "method", "GET"))
	target := argString(args, "url", "")
	if target == "" {
		return errResult("http_request: `url` is required")
	}

	var body io.Reader
	if b := argString(args, "body", ""); b != "" {
		body = strings.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return errResult("http_request: build request: " + err.Error())
	}
	for k, v := range argStringMap(args, "headers") {
		req.Header.Set(k, v)
	}
	for k, v := range argStringMap(args, "cookies") {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; dhunter/1.0)")
	}

	timeout := time.Duration(argFloat(args, "timeout", 15.0)) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := httpClient(argBool(args, "insecure", false), timeout, 5)

	resp, err := client.Do(req)
	if err != nil {
		return errResult("http_request: " + err.Error())
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	hdr := map[string][]string(resp.Header)
	out, _ := json.Marshal(map[string]interface{}{
		"status":    resp.StatusCode,
		"proto":     resp.Proto,
		"headers":   hdr,
		"body":      string(respBody),
		"body_size": len(respBody),
		"final_url": resp.Request.URL.String(),
		"truncated": len(respBody) >= 4<<20,
	})
	return textResult(string(out))
}

// apiFuzz mutates one parameter across a wordlist and reports which values
// produce a response that differs from the baseline.
func apiFuzz(ctx context.Context, args map[string]interface{}) toolResult {
	target := argString(args, "url", "")
	if target == "" {
		return errResult("api_fuzz: `url` is required")
	}
	wordlist := argStringSlice(args, "wordlist")
	if len(wordlist) == 0 {
		return errResult("api_fuzz: `wordlist` is required")
	}
	paramName := argString(args, "param_name", "fuzz")
	paramLoc := argString(args, "param_loc", "query")
	if paramLoc != "query" && paramLoc != "body" && paramLoc != "header" {
		return errResult("api_fuzz: `param_loc` must be query|body|header")
	}
	concurrency := argInt(args, "concurrency", 5)
	if concurrency > 20 {
		concurrency = 20
	}
	client := httpClient(argBool(args, "insecure", false), 10*time.Second, 3)

	// buildRequest constructs a request for the given param_loc with the
	// param set to `value`. Used for both the baseline (empty value) and the
	// fuzz variants so the comparison is apples-to-apples.
	buildRequest := func(value string) (*http.Request, error) {
		var (
			u    = target
			meth = "GET"
			bd   io.Reader
			ct   string
		)
		switch paramLoc {
		case "query":
			if pu, err := url.Parse(target); err == nil {
				q := pu.Query()
				q.Set(paramName, value)
				pu.RawQuery = q.Encode()
				u = pu.String()
			}
		case "body":
			meth = "POST"
			ct = "application/x-www-form-urlencoded"
			bd = strings.NewReader(url.Values{paramName: {value}}.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, meth, u, bd)
		if err != nil {
			return nil, err
		}
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		if paramLoc == "header" {
			req.Header.Set(paramName, value)
		}
		return req, nil
	}

	// baseline — same method/loc as the fuzz variants (empty param value).
	baseStatus, baseSize := 0, 0
	if baseReq, err := buildRequest(""); err == nil {
		if br, err := client.Do(baseReq); err == nil {
			b, _ := io.ReadAll(io.LimitReader(br.Body, 4096))
			baseStatus, baseSize = br.StatusCode, len(b)
			br.Body.Close()
		}
	}

	type hit struct {
		Payload string `json:"payload"`
		Status  int    `json:"status"`
		Size    int    `json:"size"`
		Delta   bool   `json:"delta"`
	}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		hits   []hit
		sem    = make(chan struct{}, concurrency)
	)
	for _, w := range wordlist {
		wg.Add(1)
		sem <- struct{}{}
		go func(payload string) {
			defer wg.Done()
			defer func() { <-sem }()

			req, err := buildRequest(payload)
			if err != nil {
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			status, size := resp.StatusCode, len(b)
			delta := status != baseStatus || absInt(size-baseSize) > 50
			if delta {
				mu.Lock()
				hits = append(hits, hit{Payload: payload, Status: status, Size: size, Delta: true})
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	out, _ := json.Marshal(map[string]interface{}{
		"target":     target,
		"param":      paramName,
		"loc":        paramLoc,
		"baseline":   map[string]int{"status": baseStatus, "size": baseSize},
		"fuzz_count": len(wordlist),
		"hits":       hits,
	})
	return textResult(string(out))
}

// authBypass tries a battery of path/method/header transformations and
// reports which ones get a different response from the baseline.
func authBypass(ctx context.Context, args map[string]interface{}) toolResult {
	target := argString(args, "url", "")
	if target == "" {
		return errResult("auth_bypass_check: `url` is required")
	}
	headers := argStringMap(args, "headers")
	cookies := argStringMap(args, "cookies")

	parsed, err := url.Parse(target)
	if err != nil {
		return errResult("auth_bypass_check: bad url")
	}
	origPath := parsed.Path

	type trial struct {
		Method string
		Path   string
		Extra  map[string]string
		Note   string
	}
	trials := []trial{
		{"GET", origPath, nil, "baseline"},
		{"GET", origPath + "/", nil, "trailing slash"},
		{"GET", origPath + ".json", nil, "json extension"},
		{"GET", origPath + "%2e", nil, "url-encoded dot"},
		{"GET", origPath + "%20", nil, "url-encoded space"},
		{"GET", origPath + "%00", nil, "null byte"},
		{"GET", origPath + "..;/", nil, "path traversal"},
		{"GET", "//" + parsed.Host + origPath, nil, "double-slash host"},
		{"GET", origPath, map[string]string{"X-Original-URL": origPath}, "X-Original-URL"},
		{"GET", origPath, map[string]string{"X-Rewrite-URL": origPath}, "X-Rewrite-URL"},
		{"GET", origPath, map[string]string{"X-Forwarded-For": "127.0.0.1"}, "XFF localhost"},
		{"GET", origPath, map[string]string{"X-Real-IP": "127.0.0.1"}, "X-Real-IP"},
		{"GET", origPath, map[string]string{"X-Forwarded-Host": "localhost"}, "X-Forwarded-Host"},
		{"GET", origPath, map[string]string{"Host": "localhost"}, "Host localhost"},
		{"POST", origPath, nil, "POST empty"},
		{"PUT", origPath, nil, "PUT empty"},
		{"PATCH", origPath, nil, "PATCH"},
		{"DELETE", origPath, nil, "DELETE"},
		{"OPTIONS", origPath, nil, "OPTIONS"},
		{"HEAD", origPath, nil, "HEAD"},
	}

	build := func(t trial) *http.Request {
		u2 := *parsed
		// Keep the literal path (including a leading "//") — net/http sends
		// it as the request-target verbatim, which is the point of the test.
		u2.Path = t.Path
		req, _ := http.NewRequestWithContext(ctx, t.Method, u2.String(), nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		for k, v := range cookies {
			req.AddCookie(&http.Cookie{Name: k, Value: v})
		}
		for k, v := range t.Extra {
			if strings.EqualFold(k, "Host") {
				req.Host = v // net/http ignores Header["Host"]
				continue
			}
			req.Header.Set(k, v)
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; dhunter/1.0)")
		}
		return req
	}

	client := httpClient(argBool(args, "insecure", false), 8*time.Second, 0)
	do := func(t trial) (int, int, error) {
		resp, err := client.Do(build(t))
		if err != nil {
			return -1, 0, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, len(b), nil
	}

	baseStatus, baseSize, _ := do(trials[0])
	type outcome struct {
		Trial  string `json:"trial"`
		Method string `json:"method"`
		Path   string `json:"path"`
		Status int    `json:"status"`
		Size   int    `json:"size"`
		Note   string `json:"note,omitempty"`
	}
	results := make([]outcome, 0, len(trials))
	for _, t := range trials[1:] {
		st, sz, err := do(t)
		if err != nil {
			results = append(results, outcome{Trial: t.Note, Method: t.Method, Path: t.Path, Status: -1, Note: err.Error()})
			continue
		}
		results = append(results, outcome{Trial: t.Note, Method: t.Method, Path: t.Path, Status: st, Size: sz})
	}

	out, _ := json.Marshal(map[string]interface{}{
		"target":   target,
		"baseline": map[string]int{"status": baseStatus, "size": baseSize},
		"results":  results,
		"hint":     "status==200 with size close to baseline may indicate an auth bypass; large size deltas may indicate different application logic",
	})
	return textResult(string(out))
}

// wafDetect compares a benign request with a hostile one and looks for
// WAF fingerprints in headers, cookies, status codes, and block pages.
func wafDetect(ctx context.Context, args map[string]interface{}) toolResult {
	target := argString(args, "url", "")
	if target == "" {
		return errResult("waf_detect: `url` is required")
	}
	client := httpClient(argBool(args, "insecure", false), 10*time.Second, 0)

	fetch := func(ua string) (int, http.Header, string) {
		req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
		req.Header.Set("User-Agent", ua)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, resp.Header, string(b)
	}

	benignCode, benignH, _ := fetch("Mozilla/5.0 (compatible; dhunter/1.0)")
	evilCode, _, evilBody := fetch("Mozilla/5.0 <script>alert(1)</script>' OR 1=1--")

	// fingerprint search space: headers + cookies + server banner
	haystack := benignH.Get("Server") + " " + benignH.Get("X-Powered-By")
	for k, vs := range benignH {
		haystack += " " + strings.ToLower(k) + ": " + strings.Join(vs, ", ")
	}
	haystack = strings.ToLower(haystack)

	signatures := map[string][]string{
		"cloudflare":  {"cloudflare", "cf-ray", "__cfduid"},
		"akamai":      {"akamai", "akamai-ghost"},
		"aws-waf":     {"x-amzn-requestid", "x-amz-cf-id"},
		"aliyun-waf":  {"aliyungf_tc"},
		"tencent-waf": {"tencent-cloud", "waf.tencent-cloud.net"},
		"baidu-yunjiasu": {"yunjiasu"},
		"360-waf":     {"360wzb", "x-powered-by-360wzb"},
		"safe3waf":    {"safe3waf"},
		"modsecurity": {"mod_security", "modsecurity"},
		"f5-big-ip":   {"big-ip", "f5-bigip"},
		"imperva":     {"imperva", "incapsula"},
		"sucuri":      {"sucuri", "x-sucuri-id"},
	}
	var matched []string
	for name, sigs := range signatures {
		for _, s := range sigs {
			if strings.Contains(haystack, s) {
				matched = append(matched, name)
				break
			}
		}
	}

	blocked := evilCode != 0 && evilCode != benignCode &&
		(evilCode == 403 || evilCode == 406 || evilCode == 429 || evilCode == 503)
	if !blocked && evilBody != "" {
		bl := strings.ToLower(evilBody)
		for _, sig := range []string{
			"access denied", "forbidden", "blocked", "web application firewall",
			"challenge", "verify you are human", "captcha", "anti-bot",
			"security check", "request rejected", "suspicious activity",
		} {
			if strings.Contains(bl, sig) {
				blocked = true
				break
			}
		}
	}

	out, _ := json.Marshal(map[string]interface{}{
		"target":  target,
		"benign":  benignCode,
		"evil":    evilCode,
		"blocked": blocked,
		"matched": matched,
	})
	return textResult(string(out))
}
