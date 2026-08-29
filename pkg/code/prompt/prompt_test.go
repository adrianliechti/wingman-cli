package prompt

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/model"
)

func TestVariantFor(t *testing.T) {
	def := VariantFor("")

	if def.Agent == "" || def.Plan == "" || def.Unattended == "" {
		t.Fatal("default variant has empty modes")
	}

	if v := VariantFor("some-unknown-model"); v != def {
		t.Errorf("VariantFor(some-unknown-model) = distinct variant, want default")
	}
	for _, id := range []string{"grokish-4.6", "minimaximum-m3", "gptish-5.6", "qwenish-3.8"} {
		if v := VariantFor(id); v != def {
			t.Errorf("VariantFor(%s) matched a partial family name", id)
		}
	}

	for _, id := range []string{
		"gpt-5.6-sol", "gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.2", "gpt-5.1", "gpt-4o",
		"claude-opus-5", "claude-opus-4-8", "claude-sonnet-5", "claude-sonnet-4-6", "claude-haiku-4-6", "claude-fable-5", "claude-mythos-5",
		"gemini-3.7-flash", "gemini-3.6-flash", "glm-5.3", "glm-5.2", "kimi-k3", "minimax-m3", "grok-4.6",
		"qwen3.8-max", "qwen3.8", "qwen3.7-plus", "qwen3.5-plus",
	} {
		v := VariantFor(id)

		if v.Agent == def.Agent {
			t.Errorf("VariantFor(%s).Agent = default, want family variant", id)
		}

		if v.Plan != def.Plan || v.Unattended != def.Unattended {
			t.Errorf("VariantFor(%s) plan/unattended should fall back to defaults", id)
		}
	}

	if VariantFor("gpt-5.6-sol").Agent != VariantFor("gpt-5.3-codex").Agent {
		t.Error("gpt-5.6 and GPT-5.3 models should share the default GPT prompt")
	}

	if VariantFor("gpt-5.1").Agent == VariantFor("gpt-5.6-sol").Agent {
		t.Error("GPT-5.1 should keep its model-specific prompt")
	}

	if VariantFor("gpt-5.1-codex-max").Agent != VariantFor("gpt-5.1").Agent {
		t.Error("GPT-5.1 variants should share the GPT-5.1 prompt")
	}

	if VariantFor("claude-sonnet-5").Agent != VariantFor("claude-haiku-4-6").Agent {
		t.Error("Claude Sonnet and Haiku should share the default Claude prompt")
	}

	if VariantFor("claude-opus-5").Agent == VariantFor("claude-sonnet-5").Agent {
		t.Error("Claude Opus 5 should keep its model-specific prompt")
	}
	if VariantFor("claude-opus-4-7").Agent != VariantFor("claude-opus-4-8").Agent {
		t.Error("Claude Opus 4 models should share the Opus prompt")
	}

	if VariantFor("gpt-5.5").Agent == VariantFor("gpt-5.6-sol").Agent {
		t.Error("GPT-5.5 should keep its model-specific prompt")
	}

	if VariantFor("gpt-5.4-mini").Agent == VariantFor("gpt-5.4").Agent {
		t.Error("GPT-5.4 Mini should keep its model-specific prompt")
	}
	if VariantFor("glm-5.3").Agent != VariantFor("glm-5.2").Agent {
		t.Error("GLM models should share the GLM family prompt")
	}

	if VariantFor("minimax-m2.7").Agent != VariantFor("minimax-m3").Agent {
		t.Error("MiniMax models should share the MiniMax family prompt")
	}
	if VariantFor("grok-4.5").Agent != VariantFor("grok-4.6").Agent {
		t.Error("Grok models should share the Grok family prompt")
	}
	if VariantFor("qwen3.8-max").Agent != VariantFor("qwen3.5-plus").Agent {
		t.Error("Qwen models should share the Qwen family prompt")
	}
	if VariantFor("qwen/qwen3.8-27b").Agent != VariantFor("qwen3.8").Agent {
		t.Error("provider-prefixed Qwen models should use the Qwen family prompt")
	}

	if upper := VariantFor("GPT-5.6-Sol"); upper != VariantFor("gpt-5.6-sol") {
		t.Error("VariantFor is not case-insensitive")
	}
	if upper := VariantFor("MiniMax-M3"); upper != VariantFor("minimax-m3") {
		t.Error("MiniMax M3 prompt matching is not case-insensitive")
	}
	if upper := VariantFor("QWEN3.8-MAX"); upper != VariantFor("qwen3.8-max") {
		t.Error("Qwen prompt matching is not case-insensitive")
	}
}

func TestBuildInstructionsRendersModelTemplate(t *testing.T) {
	for _, id := range []string{
		"claude-sonnet-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-fable-5",
		"claude-mythos-5",
		"gpt-5.6-sol",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.2",
		"gpt-5.1-codex",
		"gemini-3.6-flash",
		"glm-5.3",
		"kimi-k3",
		"minimax-m3",
		"grok-4.6",
		"qwen3.8",
		"qwen3.8-max",
	} {
		t.Run(id, func(t *testing.T) {
			selected, ok := model.Find(id)
			if !ok {
				t.Fatalf("model.Find(%q) failed", id)
			}

			variant := VariantFor(id)
			for _, mode := range []struct {
				name   string
				source string
			}{
				{name: "agent", source: variant.Agent},
				{name: "plan", source: variant.Plan},
			} {
				t.Run(mode.name, func(t *testing.T) {
					got := BuildInstructions(mode.source, SectionData{Model: selected})
					if !strings.Contains(got, selected.Name) || !strings.Contains(got, id) {
						t.Fatalf("rendered instructions missing model metadata: %q", got[:min(len(got), 300)])
					}
					if strings.Contains(got, "{{ .Model") {
						t.Fatal("rendered instructions still contain model template expressions")
					}
				})
			}
		})
	}
}

func TestFallbackModesRenderModelTemplate(t *testing.T) {
	selected := model.Model{ID: "future-code-model", Name: "Future Code Model"}

	for mode, source := range map[string]string{
		"agent": VariantFor(selected.ID).Agent,
		"plan":  VariantFor(selected.ID).Plan,
	} {
		t.Run(mode, func(t *testing.T) {
			got := BuildInstructions(source, SectionData{Model: selected})
			if !strings.Contains(got, selected.Name) || !strings.Contains(got, selected.ID) {
				t.Fatalf("rendered fallback instructions missing model metadata: %q", got[:min(len(got), 300)])
			}
			if strings.Contains(got, "{{ .Model") {
				t.Fatal("rendered fallback instructions still contain model template expressions")
			}
		})
	}

	withoutModel := BuildInstructions(VariantFor("").Agent, SectionData{})
	if strings.Contains(withoutModel, "You are powered by") {
		t.Error("fallback instructions render an empty model identity")
	}
}

func TestBuildInstructionsSharedSections(t *testing.T) {
	got := BuildInstructions("base\n\n# Last Model Section\n\nbody", SectionData{
		Date:                "2026-08-15",
		Timezone:            "Europe/Berlin",
		OS:                  "darwin",
		Arch:                "arm64",
		WorkingDir:          "/repo",
		Shell:               "zsh",
		MemoryDir:           "/memory",
		MemoryContent:       "- [Preferences](preferences.md) — coding preferences",
		Skills:              "<available_skills><skill><name>review</name><location>/skills/review/SKILL.md</location></skill></available_skills>",
		ProjectInstructions: "From AGENTS.md:\n\nproject rule",
	})

	for _, title := range []string{"Project Guidelines", "Skills", "Memory", "Environment"} {
		if !strings.Contains(got, "# "+title+"\n") {
			t.Errorf("instructions missing top-level %q section", title)
		}
		if strings.Contains(got, "## "+title+"\n") {
			t.Errorf("instructions nest shared %q section below the model prompt", title)
		}
	}

	for _, want := range []string{
		"deeper files override broader ones",
		"request clearly matches its description",
		"read the skill's `<location>` completely",
		"Saved memory index, newest first — a locator only",
		"Each memory is one file holding one fact",
		"Time Zone: Europe/Berlin",
		"Shell: zsh",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions missing shared guidance %q", want)
		}
	}

	boundary := strings.Index(got, BoundaryMarker)
	if boundary < 0 {
		t.Fatal("instructions missing session-context boundary")
	}
	for _, title := range []string{"Project Guidelines", "Skills", "Memory"} {
		if index := strings.Index(got, "# "+title+"\n"); index < 0 || index > boundary {
			t.Errorf("static section %q appears after session-context boundary", title)
		}
	}
	if index := strings.Index(got, "# Environment\n"); index < boundary {
		t.Error("environment section appears before session-context boundary")
	}
}

func TestBuildInstructionsAlwaysExplainsProjectInstructionScope(t *testing.T) {
	for _, id := range []string{"claude-opus-5", "gpt-5.6-sol", "gpt-5.2", "gpt-5.1-codex"} {
		t.Run(id, func(t *testing.T) {
			got := BuildInstructions(VariantFor(id).Agent, SectionData{})
			if !strings.Contains(got, "# Project Guidelines\n") || !strings.Contains(got, "check for applicable instruction files") {
				t.Fatalf("instructions missing project-file discovery policy: %q", got)
			}
			if strings.Contains(got, "## Project instruction files") {
				t.Fatal("model prompt duplicates the shared project-file policy")
			}
		})
	}
}

func TestAgentPromptPolicy(t *testing.T) {
	for _, id := range []string{
		"some-unknown-model",
		"claude-sonnet-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-fable-5",
		"claude-mythos-5",
		"gpt-5.6-sol",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.2",
		"gpt-5.1-codex",
		"gemini-3.6-flash",
		"glm-5.3",
		"kimi-k3",
		"minimax-m3",
		"grok-4.6",
		"qwen3.8",
	} {
		t.Run(id, func(t *testing.T) {
			agent := strings.ToLower(VariantFor(id).Agent)

			for _, want := range []string{"authorized security testing", "owasp"} {
				if !strings.Contains(agent, want) {
					t.Errorf("agent prompt missing software-security guidance %q", want)
				}
			}

			for _, unwanted := range []string{"they/them", "misgender", "intellectual property"} {
				if strings.Contains(agent, unwanted) {
					t.Errorf("agent prompt contains unwanted policy %q", unwanted)
				}
			}
		})
	}
}

func TestReferenceSpecificCopyrightHeaderGuidance(t *testing.T) {
	const guidance = "never add copyright or license headers unless specifically requested"

	for _, id := range []string{"gpt-5.1-codex", "gpt-5.2"} {
		t.Run(id+"/present", func(t *testing.T) {
			if agent := strings.ToLower(VariantFor(id).Agent); !strings.Contains(agent, guidance) {
				t.Errorf("agent prompt missing reference guidance %q", guidance)
			}
		})
	}

	for _, id := range []string{"gpt-5.6-sol", "gpt-5.5", "gpt-5.4", "gpt-5.4-mini"} {
		t.Run(id+"/absent", func(t *testing.T) {
			if agent := strings.ToLower(VariantFor(id).Agent); strings.Contains(agent, guidance) {
				t.Errorf("agent prompt contains guidance absent from its reference: %q", guidance)
			}
		})
	}
}

func TestFallbackAgentContract(t *testing.T) {
	agent := strings.ToLower(VariantFor("some-unknown-model").Agent)

	for _, want := range []string{
		"until their goal is genuinely handled",
		"answer, explain, review, diagnose, plan, or report status",
		"change, build, or fix",
		"destructive, difficult to reverse, externally visible",
		"preserve the user's work",
		"use the available tool descriptions as the source of truth",
		"run the narrowest relevant validation",
		"never claim a check passed unless you ran it",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("fallback agent prompt missing contract %q", want)
		}
	}

	for _, unwanted := range []string{
		"already include `lsp` errors",
		"github copilot",
		"vs code editor",
		"tool_search_tool",
	} {
		if strings.Contains(agent, unwanted) {
			t.Errorf("fallback agent prompt contains stale or vendor-specific guidance %q", unwanted)
		}
	}
}

func TestFallbackPlanContract(t *testing.T) {
	plan := strings.ToLower(VariantFor("some-unknown-model").Plan)

	for _, want := range []string{
		"planning mode is read-only",
		"do not implement",
		"recommend one approach",
		"decision-complete implementation plan",
		"resolve unknowns from code and available evidence before asking",
		"include verification that exercises the changed behavior",
		"end after presenting the plan",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("fallback plan prompt missing contract %q", want)
		}
	}

	for _, unwanted := range []string{
		"use planning mode for",
		"agent_type",
		"`grep` / `glob` / `code_graph` / `lsp`",
	} {
		if strings.Contains(plan, unwanted) {
			t.Errorf("fallback plan prompt contains unnecessary routing guidance %q", unwanted)
		}
	}
}

func TestQwenAgentContract(t *testing.T) {
	agent := strings.ToLower(VariantFor("qwen3.8").Agent)

	for _, want := range []string{
		"# core mandates",
		"# software-engineering workflow",
		"never assume a library or framework",
		"start with a reasonable approach",
		"do not retry blindly",
		"before the first tool call",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("Qwen agent prompt missing reference guidance %q", want)
		}
	}

	if agent == strings.ToLower(VariantFor("gemini-3.6-flash").Agent) {
		t.Error("Qwen should keep its model-specific prompt")
	}

	for _, unwanted := range []string{"qwen.md", "run_shell_command", "ask_user_question", "qwen:user-prompt-submit-context"} {
		if strings.Contains(agent, unwanted) {
			t.Errorf("Qwen agent prompt contains Qwen Code harness instruction %q", unwanted)
		}
	}
}

func TestNewModelPromptsExcludeVendorHarnessInstructions(t *testing.T) {
	for _, id := range []string{"gemini-3.6-flash", "glm-5.3", "kimi-k3", "minimax-m3", "grok-4.6", "qwen3.8"} {
		t.Run(id, func(t *testing.T) {
			agent := strings.ToLower(VariantFor(id).Agent)
			for _, unwanted := range []string{"github copilot", "microsoft content policies", "vs code editor", "/mnt/agents", "tool_search_tool"} {
				if strings.Contains(agent, unwanted) {
					t.Errorf("agent prompt contains vendor-specific harness instruction %q", unwanted)
				}
			}
		})
	}
}
