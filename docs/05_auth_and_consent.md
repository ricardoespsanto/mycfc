# Task: GDPR Auth, Consent Forms, and Data Deletion

Generate handlers, `templ` components, and migrations for auth/consent in European Portuguese (pt-PT).

1. **Goose Migration**: Create `00002_consent_forms.sql` for the `consent_forms` table.
2. **Handlers**: 
   * `HandleLogin`: Verify `bcrypt` hash, initialize session.
   * `HandleRegister`: Validate checkboxes. Execute a database transaction via `sqlc` inserting the user and consent records. Return 422 via HTMX if checkboxes are missing.
   * `HandleDeleteAccount`: An endpoint to process a "Right to be Forgotten" request, scrubbing PII and unlinking references in the DB.
3. **templ Components**: Create `login.templ` and `registo.templ`. Ensure all forms contain the CSRF token and use `hx-ext="response-targets"`.
