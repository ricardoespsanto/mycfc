package activity

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAESGCMVaultSealsCredentialsForOneProviderAndUser(t *testing.T) {
	vault, err := NewAESGCMVault(bytes.Repeat([]byte{7}, 32), "activity-v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := vault.Seal(context.Background(), Provider("polar"), "person-a", NewSecret([]byte(`{"access_token":"private"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed.Ciphertext), "private") || sealed.KeyID != "activity-v1" {
		t.Fatalf("sealed credentials leaked plaintext or key id: %#v", sealed)
	}
	opened, err := vault.Open(context.Background(), Provider("polar"), "person-a", sealed)
	if err != nil || string(opened.Bytes()) != `{"access_token":"private"}` {
		t.Fatalf("opened credentials = %q, %v", opened.Bytes(), err)
	}
	if _, err := vault.Open(context.Background(), Provider("polar"), "person-b", sealed); err == nil {
		t.Fatal("credentials opened for a different person")
	}
	if _, err := vault.Open(context.Background(), Provider("garmin"), "person-a", sealed); err == nil {
		t.Fatal("credentials opened for a different provider")
	}
}

func TestAESGCMVaultRejectsInvalidConfigurationAndCiphertext(t *testing.T) {
	if _, err := NewAESGCMVault([]byte("short"), "activity-v1"); err == nil {
		t.Fatal("short key accepted")
	}
	vault, err := NewAESGCMVault(bytes.Repeat([]byte{3}, 32), "activity-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Open(context.Background(), Provider("polar"), "person-a", SealedCredentials{Ciphertext: []byte("invalid"), KeyID: "activity-v1"}); err == nil {
		t.Fatal("malformed ciphertext accepted")
	}
}

func TestAESGCMVaultOpensEnvelopesDuringKeyRotation(t *testing.T) {
	oldVault, err := NewAESGCMVault(bytes.Repeat([]byte{1}, 32), "activity-v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := oldVault.Seal(context.Background(), Provider("polar"), "person-a", NewSecret([]byte("token")))
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewAESGCMKeyring("activity-v2", map[string][]byte{
		"activity-v1": bytes.Repeat([]byte{1}, 32),
		"activity-v2": bytes.Repeat([]byte{2}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := rotated.Open(context.Background(), Provider("polar"), "person-a", sealed)
	if err != nil || string(opened.Bytes()) != "token" {
		t.Fatalf("open rotated envelope: %q, %v", opened.Bytes(), err)
	}
	newSealed, err := rotated.Seal(context.Background(), Provider("polar"), "person-a", NewSecret([]byte("new-token")))
	if err != nil || newSealed.KeyID != "activity-v2" {
		t.Fatalf("new envelope = %#v, %v", newSealed, err)
	}
}
