# Task: Auth, Guardian/Minor Flow, and Consent

Generate handlers and templates for authentication and the specific guardian registration flow.

1. **Self-Registration (`GET/POST /registo`)**:
   * Captures Adult User data. Requires checking 'Termos_Gerais' and 'Uso_Imagem' boxes. 
   * Uses a DB transaction (`sqlc`) to insert the `User` and `ConsentForms` rows together.
2. **Guardian Dependent Flow (`POST /guardian/add-dependent`)**:
   * Only accessible by 'Guardian' role. 
   * Form requires checking the 'Responsabilidade_Menor' consent box. Must use `hx-disabled-elt`.
   * Handler inserts a new `User` (the minor) with `guardian_id` set to the logged-in Guardian's ID, and records the consent form.
3. **Login/Logout**: Implement `HandleLogin` (bcrypt verification, scs session init) and `HandleLogout` (session destroy).
