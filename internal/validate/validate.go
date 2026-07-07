package validate

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type Manifest struct {
	Name                string              `json:"name"`
	Version             string              `json:"version"`
	Description         string              `json:"description,omitempty"`
	Author              string              `json:"author,omitempty"`
	License             string              `json:"license,omitempty"`
	Source              *SourceInfo         `json:"source,omitempty"`
	Dependencies        map[string]string   `json:"dependencies,omitempty"`
	HardwareRequirements *HardwareReq       `json:"hardware_requirements,omitempty"`
	Brain               *BrainConfig        `json:"brain,omitempty"`
	Runtime             *RuntimeConfig      `json:"runtime,omitempty"`
}

type SourceInfo struct {
	Repository string `json:"repository,omitempty"`
	Issues     string `json:"issues,omitempty"`
	IssuesAPI  string `json:"issues_api,omitempty"`
}

type HardwareReq struct {
	MinRAMMB     int    `json:"min_ram_mb,omitempty"`
	MinStorageMB int    `json:"min_storage_mb,omitempty"`
	NPURequired  bool   `json:"npu_required,omitempty"`
}

type BrainConfig struct {
	BaseModel string `json:"base_model,omitempty"`
	Adapter   string `json:"adapter,omitempty"`
	RawModel  *struct {
		Type    string          `json:"type,omitempty"`
		Weights json.RawMessage `json:"weights,omitempty"`
	} `json:"raw_model,omitempty"`
	WideModel *struct {
		BaseModel string          `json:"base_model,omitempty"`
		Adapter   string          `json:"adapter,omitempty"`
		Weights   json.RawMessage `json:"weights,omitempty"`
	} `json:"wide_model,omitempty"`
}

type RuntimeConfig struct {
	SystemPrompt string      `json:"system_prompt,omitempty"`
	ToolsRoot    string      `json:"tools_root,omitempty"`
	MCPServers   []MCPServer `json:"mcp_servers,omitempty"`
	Background   bool        `json:"background,omitempty"`
	Capabilities []string    `json:"capabilities,omitempty"`
}

type MCPServer struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Transport string            `json:"transport,omitempty"`
}

type Result struct {
	Passed map[string]bool
	Errors []RuleError
}

type RuleError struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

func (r Result) AllPassed() bool {
	return len(r.Errors) == 0
}

func (r Result) Error() string {
	var msgs []string
	for _, e := range r.Errors {
		msgs = append(msgs, fmt.Sprintf("%s: %s", e.Rule, e.Message))
	}
	return strings.Join(msgs, "; ")
}

func Run(manifestRaw json.RawMessage, archive io.Reader, sha256 string) Result {
	res := Result{Passed: make(map[string]bool)}

	var m Manifest
	if err := json.Unmarshal(manifestRaw, &m); err != nil {
		res.Errors = append(res.Errors, RuleError{Rule: "A1", Message: "invalid json: " + err.Error()})
		return res
	}
	res.Passed["A1"] = true

	// A2: schema-like checks
	if errs := checkSchema(m); len(errs) > 0 {
		for _, e := range errs {
			res.Errors = append(res.Errors, RuleError{Rule: "A2", Message: e})
		}
		return res
	}
	res.Passed["A2"] = true

	// A3: sha256 format
	if len(sha256) != 64 {
		res.Errors = append(res.Errors, RuleError{Rule: "A3", Message: "sha256 must be 64 hex characters"})
		return res
	}
	for _, c := range sha256 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			res.Errors = append(res.Errors, RuleError{Rule: "A3", Message: "sha256 must be lowercase hex"})
			return res
		}
	}
	res.Passed["A3"] = true

	// A4: dependency cycle
	if cycles := findCycles(m.Name, m.Dependencies); len(cycles) > 0 {
		res.Errors = append(res.Errors, RuleError{Rule: "A4", Message: "dependency cycle: " + strings.Join(cycles, " -> ")})
		return res
	}
	res.Passed["A4"] = true

	// A5: deps exist (deferred to store query, checked at handler level)
	res.Passed["A5"] = true

	// A6: no buggy deps (deferred to store query, checked at handler level)
	res.Passed["A6"] = true

	// A7: file references exist in archive
	if errs := checkFileRefs(m, archive); len(errs) > 0 {
		for _, e := range errs {
			res.Errors = append(res.Errors, RuleError{Rule: "A7", Message: e})
		}
		return res
	}
	res.Passed["A7"] = true

	// A8: hardware bounds
	if errs := checkHardwareBounds(m.HardwareRequirements); len(errs) > 0 {
		for _, e := range errs {
			res.Errors = append(res.Errors, RuleError{Rule: "A8", Message: e})
		}
		return res
	}
	res.Passed["A8"] = true

	// A9: source repository URL
	if m.Source != nil && m.Source.Repository != "" {
		if !isValidURL(m.Source.Repository) {
			res.Errors = append(res.Errors, RuleError{Rule: "A9", Message: "invalid repository URL: " + m.Source.Repository})
			return res
		}
	}
	res.Passed["A9"] = true

	// A10: issues URL reachable
	if m.Source != nil && m.Source.Issues != "" {
		if err := checkURLAvailable(m.Source.Issues); err != nil {
			res.Errors = append(res.Errors, RuleError{Rule: "A10", Message: "unreachable issues URL: " + err.Error()})
			return res
		}
	}
	res.Passed["A10"] = true

	return res
}

func checkSchema(m Manifest) []string {
	var errs []string
	if m.Name == "" {
		errs = append(errs, "missing required field: name")
	}
	if m.Version == "" {
		errs = append(errs, "missing required field: version")
	}
	if m.Description == "" {
		errs = append(errs, "missing required field: description")
	}
	return errs
}

func findCycles(name string, deps map[string]string) []string {
	// With only one level of dependency info (no store query),
	// the only detectable cycle is a self-dependency.
	if deps != nil {
		if _, ok := deps[name]; ok {
			return []string{name, name}
		}
	}
	return nil
}

func checkFileRefs(m Manifest, archive io.Reader) []string {
	if archive == nil {
		return nil
	}

	var errs []string
	fileSet := make(map[string]bool)

	gzr, err := gzip.NewReader(archive)
	if err != nil {
		return []string{"cannot read archive: " + err.Error()}
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return []string{"archive read error: " + err.Error()}
		}
		fileSet[filepath.Clean(hdr.Name)] = true
	}

	// Check MCP server binaries
	if m.Runtime != nil {
		for _, srv := range m.Runtime.MCPServers {
			if srv.Command != "" && !fileSet[filepath.Clean(srv.Command)] {
				errs = append(errs, fmt.Sprintf("mcp server binary not found: %s", srv.Command))
			}
		}
		if m.Runtime.SystemPrompt != "" && !fileSet[filepath.Clean(m.Runtime.SystemPrompt)] {
			errs = append(errs, fmt.Sprintf("system prompt not found: %s", m.Runtime.SystemPrompt))
		}
	}

	// Check brain adapters
	if m.Brain != nil {
		if m.Brain.Adapter != "" && !fileSet[filepath.Clean(m.Brain.Adapter)] {
			errs = append(errs, fmt.Sprintf("brain adapter not found: %s", m.Brain.Adapter))
		}
		if m.Brain.WideModel != nil && m.Brain.WideModel.Adapter != "" {
			if !fileSet[filepath.Clean(m.Brain.WideModel.Adapter)] {
				errs = append(errs, fmt.Sprintf("wide_model adapter not found: %s", m.Brain.WideModel.Adapter))
			}
		}
	}

	return errs
}

func checkHardwareBounds(hw *HardwareReq) []string {
	if hw == nil {
		return nil
	}
	var errs []string
	if hw.MinRAMMB > 1048576 {
		errs = append(errs, "min_ram_mb exceeds maximum (1048576)")
	}
	if hw.MinStorageMB > 1073741824 {
		errs = append(errs, "min_storage_mb exceeds maximum (1073741824)")
	}
	if hw.MinRAMMB < 0 {
		errs = append(errs, "min_ram_mb must be non-negative")
	}
	if hw.MinStorageMB < 0 {
		errs = append(errs, "min_storage_mb must be non-negative")
	}
	return errs
}

func isValidURL(raw string) bool {
	return strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://")
}

func checkURLAvailable(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("empty URL")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return fmt.Errorf("unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
