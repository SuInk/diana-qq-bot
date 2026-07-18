package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v4"
)

const skillFileName = "SKILL.md"

type SkillMetadata struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	Path             string `json:"path"`
	ShortDescription string `json:"short_description,omitempty"`
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Metadata    struct {
		ShortDescription string `yaml:"short-description"`
	} `yaml:"metadata"`
}

// LoadSkills scans configured roots for local SKILL.md skill folders.
func LoadSkills(roots []string) ([]SkillMetadata, error) {
	var skills []SkillMetadata
	var errs []string
	seen := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			errs = append(errs, fmt.Sprintf("%s: %v", root, err))
			continue
		}
		if !info.IsDir() {
			continue
		}
		if filepath.Base(root) == skillFileName {
			if skill, err := parseSkill(root); err == nil {
				if !seen[skill.Path] {
					seen[skill.Path] = true
					skills = append(skills, skill)
				}
			} else {
				errs = append(errs, fmt.Sprintf("%s: %v", root, err))
			}
			continue
		}
		if direct := filepath.Join(root, skillFileName); fileExists(direct) {
			if skill, err := parseSkill(direct); err == nil {
				if !seen[skill.Path] {
					seen[skill.Path] = true
					skills = append(skills, skill)
				}
			} else {
				errs = append(errs, fmt.Sprintf("%s: %v", direct, err))
			}
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", root, err))
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name(), skillFileName)
			if !fileExists(path) {
				continue
			}
			skill, err := parseSkill(path)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			if !seen[skill.Path] {
				seen[skill.Path] = true
				skills = append(skills, skill)
			}
		}
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name == skills[j].Name {
			return skills[i].Path < skills[j].Path
		}
		return skills[i].Name < skills[j].Name
	})
	if len(errs) > 0 {
		return skills, errors.New(strings.Join(errs, "; "))
	}
	return skills, nil
}

func parseSkill(path string) (SkillMetadata, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return SkillMetadata{}, err
	}
	frontmatter, err := parseSkillFrontmatter(string(body))
	if err != nil {
		return SkillMetadata{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return SkillMetadata{}, err
	}
	skill := SkillMetadata{
		Name:             strings.TrimSpace(frontmatter.Name),
		Description:      strings.TrimSpace(frontmatter.Description),
		ShortDescription: strings.TrimSpace(frontmatter.Metadata.ShortDescription),
		Path:             abs,
	}
	if skill.Name == "" {
		return SkillMetadata{}, errors.New("missing skill name")
	}
	if skill.Description == "" {
		return SkillMetadata{}, errors.New("missing skill description")
	}
	return skill, nil
}

func parseSkillFrontmatter(markdown string) (skillFrontmatter, error) {
	trimmed := strings.TrimLeft(markdown, "\ufeff\r\n\t ")
	if !strings.HasPrefix(trimmed, "---") {
		return skillFrontmatter{}, errors.New("missing YAML frontmatter")
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 {
		return skillFrontmatter{}, errors.New("incomplete YAML frontmatter")
	}
	var yamlLines []string
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			var frontmatter skillFrontmatter
			if err := yaml.Unmarshal([]byte(strings.Join(yamlLines, "\n")), &frontmatter); err != nil {
				return skillFrontmatter{}, err
			}
			return frontmatter, nil
		}
		yamlLines = append(yamlLines, lines[i])
	}
	return skillFrontmatter{}, errors.New("unterminated YAML frontmatter")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// RenderSkillsPrompt returns the compact skills catalog that the Agent keeps in context.
func RenderSkillsPrompt(skills []SkillMetadata, budget int) string {
	if len(skills) == 0 {
		return ""
	}
	if budget <= 0 {
		budget = DefaultSkillsListBudget
	}
	var builder strings.Builder
	builder.WriteString("## Skills\n")
	builder.WriteString("A skill is a set of instructions provided through a `SKILL.md` source. Below is the list of skills that can be used. Each entry includes a name, description, and source locator.\n")
	builder.WriteString("### Available skills\n")
	for _, skill := range skills {
		line := fmt.Sprintf("- %s: %s (file: %s)\n", skill.Name, skill.Description, skill.Path)
		if builder.Len()+len(line) > budget {
			builder.WriteString("- ... additional skills omitted because the skills context budget was reached.\n")
			break
		}
		builder.WriteString(line)
	}
	builder.WriteString("### How to use skills\n")
	builder.WriteString("- If the user names a skill with `$SkillName`, or the task clearly matches a skill description, use that skill for this turn.\n")
	builder.WriteString("- Before using a skill, call `skills.read` for its name and follow the full `SKILL.md` instructions.\n")
	builder.WriteString("- When a `SKILL.md` references relative files, resolve them relative to the skill file directory.\n")
	return strings.TrimSpace(builder.String())
}

func SelectExplicitSkills(skills []SkillMetadata, text string) []SkillMetadata {
	if strings.TrimSpace(text) == "" || len(skills) == 0 {
		return nil
	}
	var selected []SkillMetadata
	for _, skill := range skills {
		if hasExplicitSkillMention(text, skill.Name) {
			selected = append(selected, skill)
		}
	}
	return selected
}

func hasExplicitSkillMention(text, name string) bool {
	if name == "" {
		return false
	}
	pattern := regexp.MustCompile(`\$` + regexp.QuoteMeta(name))
	matches := pattern.FindAllStringIndex(text, -1)
	for _, match := range matches {
		beforeOK := match[0] == 0 || !isSkillNameRune(rune(text[match[0]-1]))
		afterIndex := match[1]
		afterOK := afterIndex >= len(text) || !isSkillNameRune(rune(text[afterIndex]))
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isSkillNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

type SkillTools struct {
	List *SkillsListTool
	Read *SkillsReadTool
}

func NewSkillTools(skills []SkillMetadata) SkillTools {
	copied := append([]SkillMetadata(nil), skills...)
	return SkillTools{
		List: &SkillsListTool{skills: copied},
		Read: &SkillsReadTool{skills: copied},
	}
}

type SkillsListTool struct {
	skills []SkillMetadata
}

func (t *SkillsListTool) Name() string {
	return "skills.list"
}

func (t *SkillsListTool) Description() string {
	return `列出可用本地 SKILL.md skills。input: {}`
}

func (t *SkillsListTool) Run(context.Context, map[string]any) (string, error) {
	body, err := json.MarshalIndent(map[string]any{"skills": t.skills}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type SkillsReadTool struct {
	skills []SkillMetadata
}

func (t *SkillsReadTool) Name() string {
	return "skills.read"
}

func (t *SkillsReadTool) Description() string {
	return `读取某个 skill 的完整 SKILL.md。input: {"name":"skill 名称"}`
}

func (t *SkillsReadTool) Run(_ context.Context, input map[string]any) (string, error) {
	name := stringFromInput(input, "name")
	if name == "" {
		return "", errors.New("name is required")
	}
	for _, skill := range t.skills {
		if skill.Name != name {
			continue
		}
		body, err := os.ReadFile(skill.Path)
		if err != nil {
			return "", err
		}
		payload := map[string]any{
			"name":    skill.Name,
			"path":    skill.Path,
			"content": string(body),
		}
		encoded, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
	return "", fmt.Errorf("skill %q not found", name)
}
