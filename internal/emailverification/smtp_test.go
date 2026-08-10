package emailverification

import (
	"strings"
	"testing"
)

func TestPasswordResetMessageIsPortugueseAndContainsNoAccountData(t *testing.T) {
	link := "https://mycfc.example/recuperar-palavra-passe/repor?token=opaque"
	subject, plain, rich := passwordResetMessage(link)
	for name, body := range map[string]string{"subject": subject, "plain": plain, "html": rich} {
		for _, expected := range []string{"palavra-passe"} {
			if !strings.Contains(body, expected) {
				t.Errorf("%s body does not contain %q: %q", name, expected, body)
			}
		}
	}
	for _, body := range []string{plain, rich} {
		for _, expected := range []string{link, "60 minutos", "ignore esta mensagem"} {
			if !strings.Contains(body, expected) {
				t.Errorf("body does not contain %q: %q", expected, body)
			}
		}
		for _, sensitive := range []string{"member@example.test", "nome", "número de sócio"} {
			if strings.Contains(strings.ToLower(body), sensitive) {
				t.Errorf("body contains sensitive account data %q", sensitive)
			}
		}
	}
}
