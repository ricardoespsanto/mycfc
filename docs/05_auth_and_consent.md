# Task: User Authentication and Consent Forms (pt-PT)

Generate the Go handlers, HTML templates, and SQLite schema updates for user registration and login. All user-facing UI text must be strictly in European Portuguese (pt-PT).

1. **Schema Updates**:
   - Add password hashing (using `golang.org/x/crypto/bcrypt`) to the `User` model.
   - Create a `ConsentForm` struct and table: `ID`, `UserID`, `ConsentType` (e.g., Termos e Condições, Uso de Imagem, Responsabilidade de Iniciantes), `IsAccepted` (Boolean), `DateSigned`.

2. **Go Handlers**:
   - `HandleLogin`: Parses Email and Palavra-passe, verifies the hash, and sets a secure HttpOnly session cookie.
   - `HandleRegister`: Captures registration details and explicitly requires the validation of consent form checkboxes before creating the user record.
   
3. **HTMX Templates**:
   - Create `login.html` and `registo.html`.
   - Build a specific consent capture partial (`consentimento.html`) using PicoCSS form elements. Ensure proper error validation messaging via HTMX swaps (e.g., '<small class='error'>Por favor, aceite os termos para continuar.</small>').
