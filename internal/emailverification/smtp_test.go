package emailverification

import (
	"bufio"
	"context"
	"io"
	"mime/quotedprintable"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestSMTPConfigurationRejectsUnknownTLSAndClassifiesProtocolFailures(t *testing.T) {
	if _, err := NewSMTPSender(SMTPConfig{Host: "localhost", Port: 25, Timeout: time.Second, TLSMode: "unsupported"}); err == nil {
		t.Fatal("unsupported TLS mode was accepted")
	}
	for _, mode := range []string{"none", "starttls", "implicit"} {
		sender, err := NewSMTPSender(SMTPConfig{Host: "localhost", Port: 25, Timeout: time.Second, TLSMode: mode, FromAddress: "no-reply@example.test"})
		if err != nil || sender.Client == nil {
			t.Fatalf("mode=%s sender=%#v error=%v", mode, sender, err)
		}
	}
	if !IsPermanent(&textproto.Error{Code: 550, Msg: "mailbox unavailable"}) || IsPermanent(&textproto.Error{Code: 450, Msg: "try again"}) || IsPermanent(&textproto.Error{Code: 250, Msg: "ok"}) {
		t.Fatal("SMTP protocol failure classification is incorrect")
	}
}

func TestSMTPSenderDeliversVerificationAndPasswordResetMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		send    func(*SMTPSender) error
		headers []string
		body    []string
	}{
		{
			name: "verification",
			send: func(sender *SMTPSender) error {
				return sender.SendVerification(context.Background(), "member@example.test", "https://mycfc.example/confirm?token=opaque", time.Now())
			},
			headers: []string{"To: <member@example.test>", "Confirme o seu email no MyCFCoimbra"},
			body:    []string{"https://mycfc.example/confirm?token=opaque", "válido durante 24 horas"},
		},
		{
			name: "password reset",
			send: func(sender *SMTPSender) error {
				return sender.SendPasswordReset(context.Background(), "member@example.test", "https://mycfc.example/reset?token=opaque", time.Now())
			},
			headers: []string{"To: <member@example.test>", "Recupere a sua palavra-passe no MyCFCoimbra"},
			body:    []string{"https://mycfc.example/reset?token=opaque", "válido durante 60 minutos"},
		},
		{
			name: "privacy notification",
			send: func(sender *SMTPSender) error {
				return sender.SendPrivacyNotification(context.Background(), "member@example.test", "https://mycfc.example/legal/direitos?source=email", "PRIVACY_DECISION")
			},
			headers: []string{"To: <member@example.test>", "Subject:"},
			body:    []string{"https://mycfc.example/legal/direitos?source=email", "não confirma que os dados foram apagados"},
		},
		{
			name: "guardian renewal reminder",
			send: func(sender *SMTPSender) error {
				return sender.SendGuardianRenewalReminder(context.Background(), "guardian@example.test", "https://mycfc.example/dashboard/guardian", "GUARDIAN_RENEWAL_30_DAY", time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC))
			},
			headers: []string{"To: <guardian@example.test>", "Subject:"},
			body:    []string{"https://mycfc.example/dashboard/guardian", "30 dias", "não prolonga o acesso automaticamente"},
		},
		{
			name: "guardian age handoff",
			send: func(sender *SMTPSender) error {
				return sender.SendGuardianAgeHandoff(context.Background(), "young@example.test", "https://mycfc.example/transicao-18/verificar?token=opaque", "GUARDIAN_AGE_18_EMAIL_VERIFY", time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC))
			},
			headers: []string{"To: <young@example.test>", "Confirme o email da sua conta MyCFCoimbra"},
			body:    []string{"https://mycfc.example/transicao-18/verificar?token=opaque", "só pode ser utilizado uma vez"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, message := smtpCapture(t)
			sender, err := NewSMTPSender(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.send(sender); err != nil {
				t.Fatal(err)
			}
			body := message()
			for _, want := range tc.headers {
				if !strings.Contains(body, want) {
					t.Errorf("delivered message missing %q: %s", want, body)
				}
			}
			decoded := decodeSMTPBody(t, body)
			for _, want := range tc.body {
				if !strings.Contains(decoded, want) {
					t.Errorf("decoded body missing %q: %s", want, decoded)
				}
			}
		})
	}
}

func TestGuardianSMTPRejectsUnsupportedKindsBeforeDelivery(t *testing.T) {
	sender, err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: 1, Timeout: time.Millisecond, TLSMode: "none", FromAddress: "no-reply@example.test", FromName: "MyCFCoimbra"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.SendGuardianRenewalReminder(t.Context(), "guardian@example.test", "https://mycfc.example/dashboard/guardian", "UNKNOWN", time.Now()); err == nil {
		t.Fatal("unsupported renewal reminder was accepted")
	}
	if err := sender.SendGuardianAgeHandoff(t.Context(), "young@example.test", "https://mycfc.example/transicao-18", "UNKNOWN", time.Now()); err == nil {
		t.Fatal("unsupported age handoff was accepted")
	}
	invalidFrom := *sender
	invalidFrom.FromAddress = "invalid\nfrom@example.test"
	if err := invalidFrom.SendGuardianRenewalReminder(t.Context(), "guardian@example.test", "https://mycfc.example/dashboard/guardian", "GUARDIAN_RENEWAL_7_DAY", time.Now()); err == nil {
		t.Fatal("invalid sender was accepted for guardian reminder")
	}
}

func TestPrivacySMTPRejectsMessageConstructionBeforeDelivery(t *testing.T) {
	sender, err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: 1, Timeout: time.Millisecond, TLSMode: "none", FromAddress: "no-reply@example.test", FromName: "MyCFCoimbra"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		recipient string
		kind      string
		configure func(*SMTPSender)
	}{
		{name: "unsupported kind", recipient: "member@example.test", kind: "UNKNOWN"},
		{name: "invalid from", recipient: "member@example.test", kind: "PRIVACY_DECISION", configure: func(s *SMTPSender) { s.FromAddress = "invalid\nfrom@example.test" }},
		{name: "invalid recipient", recipient: "invalid\nrecipient@example.test", kind: "PRIVACY_DECISION"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := *sender
			if tc.configure != nil {
				tc.configure(&candidate)
			}
			if err := candidate.SendPrivacyNotification(context.Background(), tc.recipient, "https://mycfc.example/legal/direitos", tc.kind); err == nil {
				t.Fatal("invalid privacy email was accepted")
			}
		})
	}
}

func decodeSMTPBody(t *testing.T, message string) string {
	t.Helper()
	_, encoded, found := strings.Cut(message, "\r\n\r\n")
	if !found {
		t.Fatal("SMTP message has no header/body separator")
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return string(decoded)
}

func smtpCapture(t *testing.T) (SMTPConfig, func() string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	message := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		defer listener.Close()
		conn, err := listener.Accept()
		if err != nil {
			errs <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		write := func(line string) error {
			if _, err := writer.WriteString(line + "\r\n"); err != nil {
				return err
			}
			return writer.Flush()
		}
		if err := write("220 localhost MyCFCoimbra test SMTP"); err != nil {
			errs <- err
			return
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errs <- err
				return
			}
			command := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(command, "EHLO "), strings.HasPrefix(command, "HELO "):
				if err := write("250 localhost"); err != nil {
					errs <- err
					return
				}
			case command == "NOOP", command == "RSET":
				if err := write("250 ready"); err != nil {
					errs <- err
					return
				}
			case strings.HasPrefix(command, "MAIL FROM:"), strings.HasPrefix(command, "RCPT TO:"):
				if err := write("250 accepted"); err != nil {
					errs <- err
					return
				}
			case command == "DATA":
				if err := write("354 send message"); err != nil {
					errs <- err
					return
				}
				var body strings.Builder
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						errs <- err
						return
					}
					if line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
				message <- body.String()
				if err := write("250 queued"); err != nil {
					errs <- err
					return
				}
			case command == "QUIT":
				_ = write("221 goodbye")
				return
			default:
				errs <- &textproto.Error{Code: 500, Msg: "unexpected SMTP command " + command}
				return
			}
		}
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return SMTPConfig{Host: host, Port: mustSMTPPort(t, port), Timeout: time.Second, TLSMode: "none", FromAddress: "no-reply@example.test", FromName: "MyCFCoimbra"}, func() string {
		select {
		case err := <-errs:
			t.Fatal(err)
		case body := <-message:
			return body
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for delivered SMTP message")
		}
		return ""
	}
}

func mustSMTPPort(t *testing.T, raw string) int {
	t.Helper()
	port, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
