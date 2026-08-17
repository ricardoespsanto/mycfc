package validation

import (
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type FieldErrors map[string]string

func (f FieldErrors) Add(field, message string) {
	if _, exists := f[field]; !exists {
		f[field] = message
	}
}

func (f FieldErrors) Has(field string) bool {
	_, exists := f[field]
	return exists
}

func (f FieldErrors) Empty() bool { return len(f) == 0 }

func NormalizeName(raw string) (string, error) {
	name := strings.Join(strings.Fields(raw), " ")
	length := utf8.RuneCountInString(name)
	if length < 2 || length > 120 {
		return "", errors.New("O nome deve ter entre 2 e 120 caracteres.")
	}
	return name, nil
}

func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > 254 {
		return "", errors.New("Introduza um endereço de correio eletrónico válido.")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || strings.ContainsAny(email, "\r\n") {
		return "", errors.New("Introduza um endereço de correio eletrónico válido.")
	}
	return email, nil
}

func ParseISODate(raw string) (time.Time, error) {
	value, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, errors.New("Introduza uma data válida.")
	}
	return value, nil
}

func AgeOn(dateOfBirth, now time.Time, location *time.Location) int {
	birth := dateOfBirth.In(location)
	today := now.In(location)
	age := today.Year() - birth.Year()
	if today.Month() < birth.Month() || (today.Month() == birth.Month() && today.Day() < birth.Day()) {
		age--
	}
	return age
}

func ValidateAdultDateOfBirth(dateOfBirth, now time.Time, location *time.Location) error {
	birth := dateOfBirth.In(location)
	today := dateOnly(now.In(location), location)
	if birth.After(today) || AgeOn(birth, today, location) < 18 {
		return errors.New("Tem de ter pelo menos 18 anos para criar uma conta.")
	}
	return nil
}

func ValidateDependentDateOfBirth(dateOfBirth, now time.Time, location *time.Location) error {
	birth := dateOfBirth.In(location)
	today := dateOnly(now.In(location), location)
	if birth.After(today) {
		return errors.New("A data de nascimento não pode estar no futuro.")
	}
	if AgeOn(birth, today, location) >= 18 {
		return errors.New("O menor a cargo tem de ter menos de 18 anos.")
	}
	return nil
}

func ValidatePassword(password string) error {
	if !utf8.ValidString(password) {
		return errors.New("A palavra-passe contém caracteres inválidos.")
	}
	byteLength := len([]byte(password))
	if byteLength < 12 || byteLength > 72 {
		return errors.New("A palavra-passe deve ter entre 12 e 72 bytes.")
	}
	var hasLetter, hasNonLetter bool
	for _, r := range password {
		if unicode.IsLetter(r) {
			hasLetter = true
		} else {
			hasNonLetter = true
		}
	}
	if !hasLetter || !hasNonLetter {
		return errors.New("A palavra-passe deve conter pelo menos uma letra e um carácter que não seja uma letra.")
	}
	return nil
}

var allowedNextPaths = map[string]struct{}{
	"/dashboard":             {},
	"/today":                 {},
	"/perfil":                {},
	"/dashboard/competitor":  {},
	"/dashboard/initiation":  {},
	"/dashboard/competition": {},
	"/dashboard/kayak-polo":  {},
	"/dashboard/leisure":     {},
	"/dashboard/guardian":    {},
	"/dashboard/coach":       {},
	"/dashboard/moderator":   {},
	"/admin/fleet":           {},
	"/admin/sistema":         {},
	"/admin/membros":         {},
	"/admin/membros/criar":   {},
	"/admin/eventos":         {},
	"/admin/avisos":          {},
	"/admin/treinos":         {},
}

func SafeNext(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if _, ok := allowedNextPaths[u.EscapedPath()]; !ok {
		return ""
	}
	return u.EscapedPath()
}

func dateOnly(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}
