package prompt

import "testing"

func TestVariantFor(t *testing.T) {
	def := VariantFor("")

	if def.Agent == "" || def.Plan == "" || def.Unattended == "" {
		t.Fatal("default variant has empty modes")
	}

	if v := VariantFor("claude-opus-4-8"); v != def {
		t.Errorf("VariantFor(claude-opus-4-8) = distinct variant, want default")
	}

	if v := VariantFor("some-unknown-model"); v != def {
		t.Errorf("VariantFor(some-unknown-model) = distinct variant, want default")
	}

	for _, id := range []string{"gpt-5.6-sol", "gpt-5.1", "gpt-4o", "claude-opus-5", "claude-sonnet-5", "claude-sonnet-4-6", "claude-haiku-4-6", "claude-fable-5", "claude-mythos-5", "gemini-2.5-pro"} {
		v := VariantFor(id)

		if v.Agent == def.Agent {
			t.Errorf("VariantFor(%s).Agent = default, want family variant", id)
		}

		if v.Plan != def.Plan || v.Unattended != def.Unattended {
			t.Errorf("VariantFor(%s) plan/unattended should fall back to defaults", id)
		}
	}

	if VariantFor("gpt-5.6-sol").Agent == VariantFor("gpt-5.1").Agent {
		t.Error("gpt-5.6 should get its own variant, not the generic gpt one")
	}

	if VariantFor("claude-sonnet-5").Agent == VariantFor("claude-sonnet-4-6").Agent {
		t.Error("claude-sonnet-5 should get its own variant, not the sonnet-4-6 one")
	}

	if VariantFor("claude-fable-5").Agent == VariantFor("claude-opus-5").Agent {
		t.Error("claude-fable-5 should get its own variant, not the opus-5 one")
	}

	if upper := VariantFor("GPT-5.6-Sol"); upper != VariantFor("gpt-5.6-sol") {
		t.Error("VariantFor is not case-insensitive")
	}
}
