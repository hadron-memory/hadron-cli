package config

import "testing"

func TestSkillsInstallPrompt(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := c.Get("skills.installPrompt"); value != "true" {
		t.Fatalf("default %q", value)
	}
	if err := c.Set("skills.installPrompt", "false"); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.SkillsInstallPrompt() {
		t.Fatal("false not persisted")
	}
	if err := c.Set("skills.installPrompt", "invalid"); err == nil {
		t.Fatal("accepted invalid boolean")
	}
	if err := c.Set("skills.installPrompt", ""); err != nil {
		t.Fatal(err)
	}
	if !c.SkillsInstallPrompt() {
		t.Fatal("unset did not restore default")
	}
}
