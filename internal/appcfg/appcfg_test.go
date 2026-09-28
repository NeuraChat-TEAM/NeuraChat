package appcfg

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"neura/internal/dpapi"
)

func TestSessionProfilesKeepMultipleImportsAndActiveChoice(t *testing.T) {
	store := &Store{dir: t.TempDir()}
	if err := store.SaveSessionProfile("user-1", "user-1", "legacy", "curl one"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionProfile("user-2", "user-2", "agent", "curl two"); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.SessionProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || !profiles[1].Active || profiles[0].Curl != "" {
		t.Fatalf("unexpected profiles: %#v", profiles)
	}
	if _, err := store.ActivateSession("user-1"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.LoadSession(); err != nil || got != "curl one" {
		t.Fatalf("active session = %q, %v", got, err)
	}
}

func TestSettingsSecretsAreProtectedAndPlaintextIsMigrated(t *testing.T) {
	store := &Store{dir: t.TempDir()}
	legacy := Defaults()
	legacy.McpServers = []McpServer{{Name: "private", ServerURL: "https://example.test/mcp", Token: "super-secret"}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(legacySettingsFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded := store.LoadSettings()
	if len(loaded.McpServers) != 1 || loaded.McpServers[0].Token != "super-secret" {
		t.Fatalf("plaintext migration did not load secrets: %#v", loaded.McpServers)
	}
	if err := store.SaveSettings(loaded); err != nil {
		t.Fatal(err)
	}
	cipher, err := os.ReadFile(store.path(settingsFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, []byte("super-secret")) {
		t.Fatal("protected settings contain plaintext secret")
	}
	if _, err := os.Stat(store.path(legacySettingsFile)); !os.IsNotExist(err) {
		t.Fatalf("legacy plaintext settings were not removed: %v", err)
	}
	loaded = store.LoadSettings()
	if loaded.McpServers[0].Token != "super-secret" {
		t.Fatal("protected settings could not be loaded")
	}
}

func TestLegacySingleSessionIsStillLoaded(t *testing.T) {
	store := &Store{dir: t.TempDir()}
	cipher, err := dpapi.Protect([]byte("legacy curl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(sessionFile), cipher, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.LoadSession(); err != nil || got != "legacy curl" {
		t.Fatalf("legacy session = %q, %v", got, err)
	}
}
