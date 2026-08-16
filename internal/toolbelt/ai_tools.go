package toolbelt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// pocScaffold returns a starting-point PoC (curl/Python) for a vuln type.
// The agent fills in the payloads and runs them through http_request.
func pocScaffold(ctx context.Context, args map[string]interface{}) toolResult {
	vulnType := strings.ToLower(argString(args, "vuln_type", ""))
	target := argString(args, "url", "")
	if vulnType == "" || target == "" {
		return errResult("poc_scaffold: `vuln_type` and `url` required")
	}
	method := argString(args, "method", "GET")
	body := argString(args, "body", "")

	tpl := ""
	switch vulnType {
	case "sqli", "sql-injection":
		tpl = fmt.Sprintf(`# SQLi PoC (manual, not sqlmap)
# 1. append ' OR 1=1-- and watch response length/status
# 2. UNION SELECT 1,2,3.. to guess column count
curl -X %s '%s%%27%%20OR%%201=1--' -i
`, method, target)
	case "xss":
		tpl = fmt.Sprintf(`# XSS PoC
# reflect: put <svg/onload=alert(1)> in a param and check it echoes unescaped
curl -X %s '%s' -H 'User-Agent: <svg/onload=alert(1)>' --data '%s' -i
`, method, target, body)
	case "ssrf":
		tpl = fmt.Sprintf(`# SSRF PoC
# try url=http://127.0.0.1/ then http://169.254.169.254/latest/meta-data/
curl -X %s '%s' --data 'url=http://127.0.0.1/' -i
`, method, target)
	case "rce", "command-injection":
		tpl = fmt.Sprintf(`# RCE / command injection PoC
# try ;id / |id / $(id); use sleep 5 for blind timing
curl -X %s '%s' --data 'cmd=%%3Bid' -i
`, method, target)
	case "lfi", "file-read":
		tpl = fmt.Sprintf(`# LFI / path traversal PoC
# try ../../../../etc/passwd and php://filter wrappers
curl -X %s '%s' -i
`, method, target)
	case "auth-bypass", "unauth":
		tpl = fmt.Sprintf(`# Auth bypass PoC
# 1. drop Authorization/Cookie and see if still 200
# 2. use the auth_bypass_check tool to automate common tricks
curl -X %s '%s' -i
`, method, target)
	case "idor":
		tpl = fmt.Sprintf(`# IDOR PoC
# swap ids 1,2,3,9999 with user A's token and check for user B's data
curl -X %s '%s' -H 'Authorization: Bearer <TOKEN>' -i
`, method, target)
	case "info-leak":
		tpl = fmt.Sprintf(`# Info leak PoC — run leak_creds first, then confirm:
curl -X GET '%s/.git/HEAD' -i
curl -X GET '%s/.env' -i
curl -X GET '%s/actuator/env' -i
`, target, target, target)
	default:
		tpl = fmt.Sprintf(`# Generic PoC for %s
curl -X %s '%s' -i
# craft the payload yourself, then verify with http_request
`, vulnType, method, target)
	}
	return textResult(tpl)
}

// riskScore is a cheap heuristic score; the agent should override it.
func riskScore(ctx context.Context, args map[string]interface{}) toolResult {
	evidence := strings.ToLower(argString(args, "evidence", ""))
	impact := strings.ToLower(argString(args, "impact", ""))
	if evidence == "" || impact == "" {
		return errResult("risk_score: `evidence` and `impact` required")
	}
	base := map[string]int{"critical": 90, "high": 70, "medium": 40, "low": 20}[impact]
	if base == 0 {
		base = 30
	}
	switch {
	case strings.Contains(evidence, "rce") || strings.Contains(evidence, "remote code"):
		base += 10
	case strings.Contains(evidence, "auth bypass") || strings.Contains(evidence, "unauth"):
		base += 5
	}
	if strings.Contains(evidence, "verified") || strings.Contains(evidence, "exploited") {
		base += 5
	}
	if base > 100 {
		base = 100
	}
	level := "info"
	switch {
	case base >= 90:
		level = "critical"
	case base >= 70:
		level = "high"
	case base >= 40:
		level = "medium"
	case base >= 20:
		level = "low"
	}
	out, _ := json.Marshal(map[string]interface{}{
		"impact": impact, "score": base, "level": level,
		"note": "heuristic only; the agent should judge from context, not trust this blindly",
	})
	return textResult(string(out))
}

// writeFinding posts a confirmed finding to the Dhunter platform.
//
// Unlike the old conversation-oriented flow, this talks directly to
// dhunter's /api/vulnerabilities with an explicit run_id. Findings land in
// status=pending and the platform's verifier promotes them to confirmed.
func writeFinding(ctx context.Context, args map[string]interface{}) toolResult {
	title := argString(args, "title", "")
	if title == "" {
		return errResult("write_finding: `title` required")
	}
	runID := argString(args, "run_id", "")
	if runID == "" {
		return errResult("write_finding: `run_id` required (the current run's id)")
	}
	platformURL := strings.TrimRight(argString(args, "platform_url", firstEnv("DHUNTER_PLATFORM_URL", "DHUNTER_BACKEND_URL")), "/")
	token := argString(args, "platform_token", firstEnv("DHUNTER_PLATFORM_TOKEN", "DHUNTER_BACKEND_TOKEN"))
	if platformURL == "" {
		platformURL = "http://127.0.0.1:13343"
	}

	payload := map[string]interface{}{
		"run_id":   runID,
		"title":    title,
		"severity": argString(args, "severity", "medium"),
		"target":   argString(args, "target", ""),
		"evidence": argString(args, "evidence", ""),
		"status":   "pending", // the verifier promotes pending -> confirmed
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", platformURL+"/api/vulnerabilities", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := httpClient(false, 10*time.Second, 3)
	resp, err := client.Do(req)
	if err != nil {
		return textResult(fmt.Sprintf("write_finding: platform unreachable (%v); kept payload: %s", err, body))
	}
	defer resp.Body.Close()
	respBody := make([]byte, 512)
	n, _ := resp.Body.Read(respBody)
	if resp.StatusCode == 409 {
		return textResult("write_finding: duplicate already recorded (HTTP 409)")
	}
	if resp.StatusCode >= 400 {
		return errResult(fmt.Sprintf("write_finding: platform http %d: %s", resp.StatusCode, string(respBody[:n])))
	}
	return textResult(fmt.Sprintf("write_finding: recorded (HTTP %d)", resp.StatusCode))
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
