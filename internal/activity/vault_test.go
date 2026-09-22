package activity

import (
	"context"
	"testing"
)

func TestAESGCMVaultRejectsInvalidConfigurationAndCiphertexts(t *testing.T) {
	if _, err := NewAESGCMVault(make([]byte, 31), "key"); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := NewAESGCMVault(make([]byte, 32), ""); err == nil {
		t.Fatal("empty id accepted")
	}
	var absent *AESGCMVault
	if _, err := absent.Seal(context.Background(), "polar", "member", NewSecret([]byte("token"))); err == nil {
		t.Fatal("nil vault sealed")
	}
	if _, err := absent.Open(context.Background(), "polar", "member", SealedCredentials{}); err == nil {
		t.Fatal("nil vault opened")
	}
	vault, _ := NewAESGCMVault(make([]byte, 32), "key")
	if _, err := vault.Open(context.Background(), "polar", "member", SealedCredentials{KeyID: "other"}); err == nil {
		t.Fatal("wrong key id opened")
	}
	if _, err := vault.Open(context.Background(), "polar", "member", SealedCredentials{KeyID: "key", Ciphertext: []byte("short")}); err == nil {
		t.Fatal("short ciphertext opened")
	}
}

func TestAESGCMVaultBindsCredentialToProviderAndMember(t *testing.T) {
	vault, err := NewAESGCMVault(make([]byte, 32), "activity-v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := vault.Seal(context.Background(), "polar", "member-a", NewSecret([]byte("token")))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := vault.Open(context.Background(), "polar", "member-a", sealed)
	if err != nil || string(opened.Bytes()) != "token" {
		t.Fatalf("opened=%q err=%v", opened.Bytes(), err)
	}
	if _, err = vault.Open(context.Background(), "polar", "member-b", sealed); err == nil {
		t.Fatal("credential opened for another member")
	}
	if _, err = vault.Open(context.Background(), "garmin", "member-a", sealed); err == nil {
		t.Fatal("credential opened for another provider")
	}
}
