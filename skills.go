package main

// skills.go — the skill loader and the dynamic prompt-injection layer.
//
// A skill is a directory containing SKILL.md with YAML frontmatter. Skills live
// in ~/.atria/skills/<name>/ (user) and ./.atria/skills/<name>/ (project, which
// wins). On load, each skill's name + description becomes one row in the
// capability index that is injected into the system prompt, so the model knows
// what procedures exist and asks for one by name. Loading the body is lazy:
// only when the model invokes the skill does the full text enter context.
//
// This mirrors Claude Code's skill model, where the description is what decides
// when a skill is used and the body is not loaded until invoked.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Skill is a loaded skill definition (body loaded lazily).
type Skill struct {
	Name        string
	Description string
	Version     string
	Path        string // dir containing SKILL.md
	Body        string // loaded on demand
	loaded      bool
}

// SkillManager owns the index and the session log of already-injected skills.
type SkillManager struct {
	mu       sync.RWMutex
	skills   map[string]*Skill
	injected map[string]bool // names already injected this session
}

func NewSkillManager() *SkillManager {
	return &SkillManager{
		skills:   map[string]*Skill{},
		injected: map[string]bool{},
	}
}

// Load scans both roots. A project skill with the same name shadows a user
// skill, matching Claude Code's precedence order.
func (s *SkillManager) Load(cfg *Config) []string {
	var problems []string
	roots := []string{
		joinPath(homeDir(), ".atria", "skills"),
		joinPath(cfg.CWD, ".atria", "skills"),
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // missing root is fine
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			path := filepath.Join(dir, "SKILL.md")
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			fm, body, err := parseFrontmatter(string(b))
			if err != nil {
				problems = append(problems, fmt.Sprintf("skill %s: %v", e.Name(), err))
				continue
			}
			if fm.Name == "" {
				fm.Name = e.Name()
			}
			s.mu.Lock()
			s.skills[fm.Name] = &Skill{
				Name:        fm.Name,
				Description: fm.Description,
				Version:     fm.Version,
				Path:        dir,
				Body:        body,
				loaded:      true,
			}
			s.mu.Unlock()
		}
	}
	return problems
}

// Index is the capability list injected into the system prompt. Sorted so the
// prompt is byte-stable across reloads.
func (s *SkillManager) Index() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.skills) == 0 {
		return ""
	}
	names := make([]string, 0, len(s.skills))
	for n := range s.skills {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("\n\n## Available skills\n\n")
	b.WriteString("Invoke a skill with the skill tool when a task matches its description.\n\n")
	for _, n := range names {
		sk := s.skills[n]
		fmt.Fprintf(&b, "- **%s**", sk.Name)
		if sk.Version != "" {
			fmt.Fprintf(&b, " v%s", sk.Version)
		}
		if sk.Description != "" {
			fmt.Fprintf(&b, " — %s", sk.Description)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Get returns a skill by name for injection, marking it injected-once.
func (s *SkillManager) Get(name string) (*Skill, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sk, ok := s.skills[name]
	if !ok {
		return nil, false
	}
	if !s.injected[name] {
		s.injected[name] = true
	}
	return sk, true
}

// Names lists every loaded skill (for /skills).
func (s *SkillManager) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.skills))
	for n := range s.skills {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// --- frontmatter ---

type skillFrontmatter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

// parseFrontmatter splits a SKILL.md into frontmatter and body.
func parseFrontmatter(raw string) (skillFrontmatter, string, error) {
	var fm skillFrontmatter
	if !strings.HasPrefix(raw, "---") {
		// no frontmatter: whole file is the body
		return fm, raw, nil
	}
	rest := strings.TrimPrefix(raw, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fm, "", fmt.Errorf("unterminated frontmatter")
	}
	header := rest[:end]
	body := strings.TrimLeft(rest[end+4:], "\n")
	// YAML frontmatter is a superset of what JSON parses here for flat keys;
	// parse manually to stay dependency-free and tolerant of both styles.
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, "\"")
		switch k {
		case "name":
			fm.Name = v
		case "description":
			fm.Description = v
		case "version":
			fm.Version = v
		}
	}
	return fm, body, nil
}

// linkedFile is one extra reference file inside a skill dir.
type linkedFile struct {
	Path string
	Body string
}

// loadLinked reads an additional file from a skill directory by relative path.
func (s *SkillManager) loadLinked(sk *Skill, rel string) (linkedFile, error) {
	full := filepath.Join(sk.Path, rel)
	b, err := os.ReadFile(full)
	if err != nil {
		return linkedFile{}, err
	}
	return linkedFile{Path: full, Body: string(b)}, nil
}

// homeDir resolves ~ without depending on a shell.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return "."
	}
	return h
}

// joinPath exists so mcp.go and skills.go share one helper.
func joinPath(parts ...string) string {
	return filepath.Join(parts...)
}

var _ = json.Marshal // reserved for future structured skill manifests
