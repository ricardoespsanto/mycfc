# Task: Accessibility and pt-PT Localization

Refine all `templ` components for WCAG accessibility. 

**Strict Localization Rules:**
* Enforce `<html lang="pt-PT">`.
* Absolutely no Brazilian Portuguese (pt-BR). 
* Explicit translations: Dashboard = Painel, Report Repair = Reportar Avaria, Paddles = Pagaias, Boats = Embarcações, Newsfeed = Notícias, Username = Utilizador, Password = Palavra-passe.

**Accessibility:**
* Explicit `<label>` associations.
* `aria-busy="true"` on HTMX buttons during flight.
* 422 validation errors must use `role="alert"` and high-contrast red text.
