package main

// model.go — the core state shared by the agent loop and the TUI.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is resolved once at startup from flags/env/config file.
type Config struct {
	Model      string `json:"model"`
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"api_key"`
	CWD        string `json:"cwd"`
	Approval   string `json:"approval"`     // ask | auto-edit | full-auto
	Security   bool   `json:"security"`     // --hack
	HackScope  string `json:"hack_scope"`
	MaxIter    int    `json:"max_iter"`
	MaxTokens  int    `json:"max_tokens"`
	Reasoning  string `json:"reasoning"`    // low | medium | high | max (specially optimized for max)
	Version    string `json:"-"`
}

const configPath = "~/.atria/config.json"

func loadConfig() (*Config, error) {
	c := &Config{
		Model:     "Atria-Dawn-Preview",
		BaseURL:   "https://api.atria-asi.ai/v1",
		Approval:  "ask",
		CWD:       ".",
		MaxIter:   40,
		MaxTokens: 8192,
		Reasoning: "max", // Atria ASI is specially optimized for max reasoning effort
		Version:   "1.0.0",
	}
	// config file first
	if p := expandPath(configPath); fileExists(p) {
		if b, err := os.ReadFile(p); err == nil {
			var stored Config
			if json.Unmarshal(b, &stored) == nil {
				mergeConfig(c, &stored)
			}
		}
	}
	// env overrides
	if v := os.Getenv("ATRIA_MODEL"); v != "" {
		c.Model = v
	}
	if v := os.Getenv("ATRIA_API_KEY"); v != "" {
		c.APIKey = v
	} else if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		c.APIKey = v
	}
	if v := os.Getenv("ATRIA_BASE_URL"); v != "" {
		c.BaseURL = v
	}
	if v := os.Getenv("ATRIA_REASONING"); v != "" {
		c.Reasoning = v
	}
	if wd, err := os.Getwd(); err == nil {
		c.CWD = wd
	}
	return c, nil
}

func (c *Config) save() error {
	p := expandPath(configPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

func (c *Config) CycleReasoning() string {
	switch c.Reasoning {
	case "low":
		c.Reasoning = "medium"
	case "medium":
		c.Reasoning = "high"
	case "high":
		c.Reasoning = "max"
	default:
		c.Reasoning = "low"
	}
	_ = c.save()
	return c.Reasoning
}

func (c *Config) HasKey() bool { return strings.TrimSpace(c.APIKey) != "" }

func (c *Config) MaskedKey() string {
	k := c.APIKey
	if len(k) <= 8 {
		return strings.Repeat("*", len(k))
	}
	return k[:6] + "…" + k[len(k)-4:]
}

func (c *Config) ProviderTag() string {
	if strings.Contains(c.BaseURL, "atria") {
		return "atria"
	}
	host := c.BaseURL
	if i := strings.Index(host, "//"); i >= 0 {
		host = host[i+2:]
	}
	return strings.Split(host, "/")[0]
}

func mergeConfig(dst, src *Config) {
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.BaseURL != "" {
		dst.BaseURL = src.BaseURL
	}
	if src.APIKey != "" {
		dst.APIKey = src.APIKey
	}
	if src.Approval != "" {
		dst.Approval = src.Approval
	}
	if src.CWD != "" {
		dst.CWD = src.CWD
	}
	dst.Security = src.Security
	if src.HackScope != "" {
		dst.HackScope = src.HackScope
	}
	if src.MaxIter > 0 {
		dst.MaxIter = src.MaxIter
	}
	if src.MaxTokens > 0 {
		dst.MaxTokens = src.MaxTokens
	}
	if src.Reasoning != "" {
		dst.Reasoning = src.Reasoning
	}
}

// --- helpers ---

func expandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func nowStamp() string {
	return time.Now().Format("15:04:05")
}

var _ = fmt.Sprintf
