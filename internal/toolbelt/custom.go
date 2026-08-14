// Custom tools: user-defined tools loaded from YAML at startup, so a
// customer can add their own scanners/scripts without recompiling.
//
// Each file in the custom tools dir (default ./tools/custom) defines tools
// as YAML documents:
//
//	- name: nmap_scan
//	  description: "Run an nmap TCP scan against a target"
//	  input_schema:
//	    type: object
//	    properties:
//	      target: { type: string }
//	      ports:  { type: string }
//	    required: [target]
//	  command: "nmap -sV -Pn {target} -p {ports}"
//	  timeout: 600        # seconds; optional
//	  insecure: false     # optional: skip TLS verify for http command outputs
//
// `command` is a shell template; `{argname}` placeholders are replaced by
// the stringified tool arguments. The command runs with a hard timeout and
// capped output (the same safeExec the built-in tools use). Directories
// are scanned once at startup.
package toolbelt

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type customToolDef struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description"`
	InputSchema map[string]interface{} `yaml:"input_schema"`
	Command     string                 `yaml:"command"`
	Timeout     int                    `yaml:"timeout"`
}

var (
	customTools      []customToolDef
	customToolsDir   = os.Getenv("DHUNTER_CUSTOM_TOOLS_DIR")
	argPlaceholderRe = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)
)

func init() {
	if customToolsDir == "" {
		customToolsDir = "./tools/custom"
	}
	LoadCustomTools(customToolsDir)
}

// LoadCustomTools reads every *.yaml/*.yml in dir and appends the tool
// definitions. Invalid files are skipped with a warning.
func LoadCustomTools(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No custom tools dir is fine — built-ins only.
		return
	}
	var loaded []customToolDef
	for _, e := range entries {
		if e.IsDir() || !(strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var defs []customToolDef
		if err := yaml.Unmarshal(data, &defs); err != nil {
			continue
		}
		for _, d := range defs {
			if d.Name == "" || d.Command == "" {
				continue
			}
			loaded = append(loaded, d)
		}
	}
	customTools = loaded
	if len(loaded) > 0 {
		log.Printf("toolbelt: loaded %d custom tool(s) from %s", len(loaded), dir)
	}
}

// customToolDefs returns the loaded custom defs as MCP tool definitions.
func customToolDefs() []toolDef {
	out := make([]toolDef, 0, len(customTools))
	for _, d := range customTools {
		schema := d.InputSchema
		if schema == nil {
			schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		out = append(out, toolDef{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: schema,
		})
	}
	return out
}

// callCustomTool runs a user-defined tool: render the command template with
// the arguments, then safeExec with the tool's timeout.
func callCustomTool(ctx context.Context, name string, args map[string]interface{}) toolResult {
	for _, d := range customTools {
		if d.Name != name {
			continue
		}
		cmd := renderCommand(d.Command, args)
		timeout := time.Duration(d.Timeout) * time.Second
		if timeout <= 0 {
			timeout = 120 * time.Second
		}
		out, err := safeExec(ctx, timeout, "sh", "-c", cmd)
		if err != nil {
			return errResult(fmt.Sprintf("%s: %s\n%s", name, err, out))
		}
		return textResult(out)
	}
	return errResult("unknown tool: " + name)
}

// renderCommand replaces {argname} placeholders with stringified args.
func renderCommand(tmpl string, args map[string]interface{}) string {
	return argPlaceholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		key := strings.TrimSuffix(strings.TrimPrefix(m, "{"), "}")
		v, ok := args[key]
		if !ok {
			return ""
		}
		return shellQuote(fmt.Sprintf("%v", v))
	})
}

// shellQuote wraps a value in single quotes for safe shell interpolation.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// marshalArgs is a small helper for tools that want to echo the args back.
func marshalArgs(args map[string]interface{}) string {
	b, _ := json.Marshal(args)
	return string(b)
}
