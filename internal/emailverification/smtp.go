package emailverification

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/textproto"
	"time"

	mail "github.com/wneessen/go-mail"
)

type SMTPConfig struct {
	Host, Username, Password, FromAddress, FromName, TLSMode string
	Port                                                     int
	Timeout                                                  time.Duration
}

type SMTPSender struct {
	Client                *mail.Client
	FromAddress, FromName string
}

func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	policy := mail.TLSMandatory
	options := []mail.Option{mail.WithPort(cfg.Port), mail.WithTimeout(cfg.Timeout)}
	switch cfg.TLSMode {
	case "starttls":
		policy = mail.TLSMandatory
	case "implicit":
		options = append(options, mail.WithSSL())
	case "none":
		policy = mail.NoTLS
	default:
		return nil, fmt.Errorf("unsupported SMTP TLS mode %q", cfg.TLSMode)
	}
	options = append(options, mail.WithTLSPortPolicy(policy))
	if cfg.Username != "" {
		options = append(options, mail.WithSMTPAuth(mail.SMTPAuthPlain), mail.WithUsername(cfg.Username), mail.WithPassword(cfg.Password))
	}
	client, err := mail.NewClient(cfg.Host, options...)
	if err != nil {
		return nil, err
	}
	return &SMTPSender{Client: client, FromAddress: cfg.FromAddress, FromName: cfg.FromName}, nil
}

func (s *SMTPSender) SendVerification(ctx context.Context, recipient, link string, _ time.Time) error {
	message := mail.NewMsg()
	if err := message.FromFormat(s.FromName, s.FromAddress); err != nil {
		return err
	}
	if err := message.To(recipient); err != nil {
		return err
	}
	message.Subject("Confirme o seu email no MyCFCoimbra")
	message.SetBodyString(mail.TypeTextPlain, "Confirme o seu endereço de email no MyCFCoimbra:\n\n"+link+"\n\nEste link é válido durante 24 horas. Se não pediu esta confirmação, ignore esta mensagem.\n")
	message.SetBodyString(mail.TypeTextHTML, "<p>Confirme o seu endereço de email no MyCFCoimbra.</p><p><a href=\""+html.EscapeString(link)+"\">Confirmar email</a></p><p>Este link é válido durante 24 horas. Se não pediu esta confirmação, ignore esta mensagem.</p>")
	return s.Client.DialAndSendWithContext(ctx, message)
}

func (s *SMTPSender) SendPasswordReset(ctx context.Context, recipient, link string, _ time.Time) error {
	message := mail.NewMsg()
	if err := message.FromFormat(s.FromName, s.FromAddress); err != nil {
		return err
	}
	if err := message.To(recipient); err != nil {
		return err
	}
	subject, plain, rich := passwordResetMessage(link)
	message.Subject(subject)
	message.SetBodyString(mail.TypeTextPlain, plain)
	message.SetBodyString(mail.TypeTextHTML, rich)
	return s.Client.DialAndSendWithContext(ctx, message)
}

func passwordResetMessage(link string) (string, string, string) {
	subject := "Recupere a sua palavra-passe no MyCFCoimbra"
	plain := "Recebemos um pedido para alterar a palavra-passe da sua conta MyCFCoimbra:\n\n" + link + "\n\nEste link é válido durante 60 minutos e só pode ser utilizado uma vez. Se não fez este pedido, ignore esta mensagem; a sua palavra-passe não será alterada.\n"
	rich := "<p>Recebemos um pedido para alterar a palavra-passe da sua conta MyCFCoimbra.</p><p><a href=\"" + html.EscapeString(link) + "\">Alterar palavra-passe</a></p><p>Este link é válido durante 60 minutos e só pode ser utilizado uma vez. Se não fez este pedido, ignore esta mensagem; a sua palavra-passe não será alterada.</p>"
	return subject, plain, rich
}

func (s *SMTPSender) SendPrivacyNotification(ctx context.Context, recipient, contactURL, kind string) error {
	subject, plain, rich, err := privacyNotificationMessage(kind, contactURL)
	if err != nil {
		return err
	}
	message := mail.NewMsg()
	if err := message.FromFormat(s.FromName, s.FromAddress); err != nil {
		return err
	}
	if err := message.To(recipient); err != nil {
		return err
	}
	message.Subject(subject)
	message.SetBodyString(mail.TypeTextPlain, plain)
	message.SetBodyString(mail.TypeTextHTML, rich)
	return s.Client.DialAndSendWithContext(ctx, message)
}

func (s *SMTPSender) SendGuardianRenewalReminder(ctx context.Context, recipient, dashboardURL, kind string, expiresAt time.Time) error {
	subject, plain, rich, err := guardianRenewalReminderMessage(kind, dashboardURL, expiresAt)
	if err != nil {
		return err
	}
	message := mail.NewMsg()
	if err := message.FromFormat(s.FromName, s.FromAddress); err != nil {
		return err
	}
	if err := message.To(recipient); err != nil {
		return err
	}
	message.Subject(subject)
	message.SetBodyString(mail.TypeTextPlain, plain)
	message.SetBodyString(mail.TypeTextHTML, rich)
	return s.Client.DialAndSendWithContext(ctx, message)
}

func guardianRenewalReminderMessage(kind, dashboardURL string, expiresAt time.Time) (string, string, string, error) {
	var timing string
	switch kind {
	case "GUARDIAN_RENEWAL_30_DAY":
		timing = "Faltam cerca de 30 dias"
	case "GUARDIAN_RENEWAL_7_DAY":
		timing = "Faltam cerca de 7 dias"
	default:
		return "", "", "", errors.New("unsupported guardian renewal reminder")
	}
	expiry := expiresAt.UTC().Format("02/01/2006")
	subject := "Reveja a sua representação no MyCFCoimbra"
	opening := timing + " para terminar a validade da sua representação, em " + expiry + ". Indique no MyCFCoimbra se os dados se mantêm ou se mudaram. A resposta será revista pelo clube e não prolonga o acesso automaticamente."
	plain := opening + "\n\n" + dashboardURL + "\n"
	rich := "<p>" + html.EscapeString(opening) + "</p><p><a href=\"" + html.EscapeString(dashboardURL) + "\">Rever representação</a></p>"
	return subject, plain, rich, nil
}

func (s *SMTPSender) SendGuardianAgeHandoff(ctx context.Context, recipient, actionURL, kind string, effectiveAt time.Time) error {
	subject, plain, rich, err := guardianAgeHandoffMessage(kind, actionURL, effectiveAt)
	if err != nil {
		return err
	}
	message := mail.NewMsg()
	if err := message.FromFormat(s.FromName, s.FromAddress); err != nil {
		return err
	}
	if err := message.To(recipient); err != nil {
		return err
	}
	message.Subject(subject)
	message.SetBodyString(mail.TypeTextPlain, plain)
	message.SetBodyString(mail.TypeTextHTML, rich)
	return s.Client.DialAndSendWithContext(ctx, message)
}

func guardianAgeHandoffMessage(kind, actionURL string, effectiveAt time.Time) (string, string, string, error) {
	location, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		return "", "", "", err
	}
	date := effectiveAt.In(location).Format("02/01/2006")
	var subject, opening, label string
	switch kind {
	case "GUARDIAN_AGE_18_30_DAY":
		subject = "Prepare a transição aos 18 anos no MyCFCoimbra"
		opening = "Faltam cerca de 30 dias para terminar o acesso de representação, em " + date + ". A pessoa jovem deve concluir a tarefa de transição na própria conta antes dessa data."
		label = "Consultar representação"
	case "GUARDIAN_AGE_18_7_DAY":
		subject = "A transição aos 18 anos aproxima-se no MyCFCoimbra"
		opening = "Faltam cerca de 7 dias para terminar o acesso de representação, em " + date + ". A pessoa jovem deve concluir a tarefa de transição na própria conta antes dessa data."
		label = "Consultar representação"
	case "GUARDIAN_AGE_18_EMAIL_VERIFY":
		subject = "Confirme o email da sua conta MyCFCoimbra"
		opening = "Confirme o email pessoal proposto para a transição da sua conta. O link é válido até " + date + " e só pode ser utilizado uma vez."
		label = "Confirmar email"
	default:
		return "", "", "", errors.New("unsupported guardian age handoff message")
	}
	plain := opening + "\n\n" + actionURL + "\n"
	rich := "<p>" + html.EscapeString(opening) + "</p><p><a href=\"" + html.EscapeString(actionURL) + "\">" + label + "</a></p>"
	return subject, plain, rich, nil
}

func privacyNotificationMessage(kind, contactURL string) (string, string, string, error) {
	var subject, opening string
	switch kind {
	case "PRIVACY_ACKNOWLEDGEMENT":
		subject = "Pedido de privacidade recebido no MyCFCoimbra"
		opening = "Recebemos o seu pedido relativo a dados pessoais. A apresentação do pedido não encerra a conta."
	case "PRIVACY_DECISION":
		subject = "Atualização do pedido de privacidade no MyCFCoimbra"
		opening = "Existe uma atualização do seu pedido relativo a dados pessoais. Esta mensagem não confirma que os dados foram apagados."
	case "PRIVACY_PROCESSING_STARTED":
		subject = "Tratamento do pedido de privacidade iniciado no MyCFCoimbra"
		opening = "Iniciámos o tratamento do seu pedido relativo a dados pessoais. O acesso à conta afetada pode ter terminado. Esta mensagem não confirma que os dados foram apagados."
	case "PRIVACY_COMPLETED":
		subject = "Pedido de privacidade concluído no MyCFCoimbra"
		opening = "Concluímos o tratamento do seu pedido relativo a dados pessoais. Esta mensagem não inclui dados da conta, categorias ou resultados detalhados."
	default:
		return "", "", "", errors.New("unsupported privacy notification")
	}
	// No names, categories, decisions or explanations are copied into email.
	// The public rights channel remains useful after account access has ended.
	help := "Pode consultar o pedido na sua conta enquanto tiver acesso. Para conhecer a resposta ou pedir esclarecimentos, mesmo sem acesso à conta, utilize o contacto indicado na página pública de direitos:"
	if kind == "PRIVACY_COMPLETED" {
		help = "O seguinte endereço de utilização única permite consultar um resumo durante 24 horas. Se já tiver sido utilizado ou tiver expirado, utilize o canal público de direitos."
	}
	plain := opening + "\n\n" + help + "\n\n" + contactURL + "\n"
	rich := "<p>" + opening + "</p><p>" + help + "</p><p><a href=\"" + html.EscapeString(contactURL) + "\">Exercer os meus direitos</a></p>"
	return subject, plain, rich, nil
}

func IsPermanent(err error) bool {
	var sendErr *mail.SendError
	if errors.As(err, &sendErr) {
		return !sendErr.IsTemp() && sendErr.ErrorCode() >= 500
	}
	var protocolErr *textproto.Error
	return errors.As(err, &protocolErr) && protocolErr.Code >= 500
}
