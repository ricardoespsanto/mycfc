package activity

import (
	"context"
	"testing"
)

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
