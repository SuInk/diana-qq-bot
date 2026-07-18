package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadSkillsAndReadTool 验证本地 SKILL.md skill 发现和完整读取。
func TestLoadSkillsAndReadTool(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "demo/SKILL.md", "---\nname: demo-skill\ndescription: Use this demo skill.\n---\n\nDo the demo workflow.")
	skills, err := LoadSkills([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "demo-skill" {
		t.Fatalf("skills = %#v", skills)
	}
	prompt := RenderSkillsPrompt(skills, 8000)
	if !strings.Contains(prompt, "demo-skill") || !strings.Contains(prompt, "skills.read") {
		t.Fatalf("prompt did not include skill guidance: %s", prompt)
	}
	tools := NewSkillTools(skills)
	got, err := tools.Read.Run(context.Background(), map[string]any{"name": "demo-skill"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Do the demo workflow.") {
		t.Fatalf("read output = %s", got)
	}
}

// TestSelectExplicitSkillsRequiresBoundary 验证 $skill 显式触发边界。
func TestSelectExplicitSkillsRequiresBoundary(t *testing.T) {
	skills := []SkillMetadata{{Name: "demo-skill", Description: "demo", Path: "/tmp/SKILL.md"}}
	if got := SelectExplicitSkills(skills, "run $demo-skill please"); len(got) != 1 {
		t.Fatalf("selected = %#v, want one", got)
	}
	if got := SelectExplicitSkills(skills, "run $demo-skill-extra please"); len(got) != 0 {
		t.Fatalf("selected = %#v, want none", got)
	}
}

func TestConfigDefaultsUseWorkDirSkillsAndMCPConfig(t *testing.T) {
	home := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("HOME", home)
	writeTestFile(t, home, ".codex/skills/demo/SKILL.md", "---\nname: demo\ndescription: demo\n---\n")
	writeTestFile(t, home, ".codex/config.toml", "[mcp_servers]\n")

	cfg := Config{WorkDir: workDir}.WithDefaults()
	if containsString(cfg.SkillRoots, filepath.Join(home, ".codex", "skills")) {
		t.Fatalf("SkillRoots = %#v, should not include home codex skills by default", cfg.SkillRoots)
	}
	wantSkills := filepath.Join(workDir, "skills")
	if !containsString(cfg.SkillRoots, wantSkills) {
		t.Fatalf("SkillRoots = %#v, missing %q", cfg.SkillRoots, wantSkills)
	}
	wantMCP := filepath.Join(workDir, ".mcp.json")
	if cfg.MCPConfigPath != wantMCP {
		t.Fatalf("MCPConfigPath = %q, want %q", cfg.MCPConfigPath, wantMCP)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
