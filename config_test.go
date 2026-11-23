package main

import "testing"

func TestConfigurationRequiresCredentials(t *testing.T) {
	for _, name := range []string{"CRM_USERNAME", "CRM_PASSWORD", "CRM_ADDR", "CRM_DATA_FILE", "CRM_SECURE_COOKIE"} {
		t.Setenv(name, "")
	}
	if _, err := loadConfig(); err == nil {
		t.Fatal("startup without credentials accepted")
	}
	t.Setenv("CRM_USERNAME", "test-operator")
	if _, err := loadConfig(); err == nil {
		t.Fatal("startup without password accepted")
	}
	t.Setenv("CRM_PASSWORD", "test-only-pass")
	c, err := loadConfig()
	if err != nil || c.Address != "127.0.0.1:8080" || c.DataFile != "data/crm.json" || c.SecureCookie {
		t.Fatalf("default config: %#v %v", c, err)
	}
	t.Setenv("CRM_ADDR", "127.0.0.1:9000")
	t.Setenv("CRM_DATA_FILE", "private/salon.json")
	t.Setenv("CRM_SECURE_COOKIE", "true")
	c, err = loadConfig()
	if err != nil || c.Address != "127.0.0.1:9000" || c.DataFile != "private/salon.json" || !c.SecureCookie {
		t.Fatal("configured values not honored")
	}
	t.Setenv("CRM_SECURE_COOKIE", "garbage")
	if _, err := loadConfig(); err == nil {
		t.Fatal("invalid secure cookie configuration accepted")
	}
}
