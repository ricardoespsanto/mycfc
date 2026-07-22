# 06 — WCAG 2.2 AA and European Portuguese localisation

## 1. Objective

All rendered interfaces must meet WCAG 2.2 Level AA for the implemented flows and use natural European Portuguese. Accessibility is a tested behavioural requirement, not a collection of decorative ARIA attributes.

## 2. Language and formatting

- Root language: `<html lang="pt-PT">`.
- Use `golang.org/x/text/language`/`message` or explicit tested formatters for pt-PT numbers.
- Display dates as `02/01/2006`, concise dates such as `2 de janeiro de 2006`, and times in 24-hour format, according to context.
- Convert all application times to `Europe/Lisbon` before rendering. Include timezone abbreviation when ambiguity matters.
- Database enum values are never shown directly; map them to approved pt-PT labels.
- Avoid untranslated English except proper product names such as WhatsApp.

## 3. Mandatory glossary

| Internal/English concept | Required pt-PT text |
|---|---|
| Dashboard | Painel |
| Report repair | Reportar avaria |
| Repair request | Pedido de reparação |
| Paddles | Pagaias |
| Boats | Embarcações |
| Vehicle | Viatura |
| Newsfeed | Notícias |
| Username | Utilizador (only where a username exists) |
| Email | Correio eletrónico |
| Password | Palavra-passe |
| Login | Iniciar sessão |
| Logout | Terminar sessão |
| Guardian | Encarregado de educação |
| Dependent/minor | Menor a cargo |
| Submit | Enviar |
| Cancel | Cancelar |
| Pending | Pendente |
| Under review | Em análise |
| Resolved | Resolvido |
| Maintenance | Manutenção |
| Training | Treino |
| Clean-up event | Ação de limpeza |

Do not use pt-BR terms such as “usuário”, “senha”, “cadastro”, “arquivo” for uploaded file, “time” for team, or gerund-heavy UI wording.

## 4. Structural accessibility

Every page MUST have:

- Unique descriptive `<title>`.
- One `<h1>` and logical non-skipping heading order.
- Skip link visible on focus.
- Semantic header, nav, main and footer landmarks.
- Current navigation item marked with `aria-current="page"`.
- Visible keyboard focus meeting WCAG 2.2 focus appearance requirements.
- No positive `tabindex`.
- No click-only non-button elements.
- A useful page when CSS or JavaScript fails.

## 5. Forms

- Every input has a visible `<label for>` matching a unique ID.
- Required fields communicate required state in text and `required`; do not rely on an asterisk alone.
- Help text and errors use stable IDs referenced by `aria-describedby`.
- Invalid fields set `aria-invalid="true"`.
- On 422, render a top error summary with `role="alert"`, heading “Corrija os seguintes campos”, and links to invalid inputs.
- Place keyboard focus on the error-summary heading after full-page or HTMX validation response.
- Preserve non-sensitive values. Never echo passwords or file paths.
- Error text identifies the field and corrective action; colour is not the only signal.
- Autocomplete tokens: `name`, `email`, `bday`, `current-password`, `new-password` as applicable.

## 6. HTMX dynamic behaviour

- Before a request, set `aria-busy="true"` on the form/region and replace submit text with a meaningful loading label while preserving button width where practical.
- After completion or error, remove `aria-busy` and restore text/enabled state.
- Success notifications use `role="status" aria-live="polite"`.
- Validation/server errors use `role="alert"`.
- After swaps, focus success heading, error summary, or first new meaningful heading; never reset focus to document body without purpose.
- Respect `prefers-reduced-motion`; no required information depends on animation.

## 7. Visual requirements

- Text and form-control contrast >= 4.5:1; large text >= 3:1; UI components/focus indicators >= 3:1.
- Browser zoom to 200% must not cause two-dimensional scrolling at 1280 CSS pixels except the calendar/table where an accessible alternative is provided.
- Touch targets are at least 24×24 CSS pixels, preferably 44×44.
- Validation red must be paired with icon/text and tested in light/dark browser schemes if both are supported.
- Do not override user font size.

## 8. Calendar and tables

- FullCalendar is enhancement, not the only schedule representation. Provide a “Ver calendários públicos” list fallback.
- Calendar controls have pt-PT accessible names and keyboard operation.
- Fleet table uses `<caption>`, correct column headers and responsive strategy. On small screens it may become cards, but label/value associations must remain programmatic.
- Status is represented by text, not colour alone.

## 9. Images

- Decorative images have empty alt.
- Repair evidence photos use concise contextual alt such as “Fotografia da avaria na embarcação CFC-012”; do not repeat surrounding caption.
- Never use filename as alt text.

## 10. Automated and manual tests

Create Playwright tests with `@axe-core/playwright` for every page state: empty, populated, validation failure, success, 403, 404 and 500.

Automated acceptance:

- No axe serious or critical violations.
- HTML validator has no duplicate IDs or invalid label references.
- Keyboard-only tests can complete every form and dashboard navigation.
- At 320 CSS-pixel viewport, no page-wide horizontal overflow.
- 200% zoom flow remains operable.
- Reduced-motion setting produces no animated scrolling.
- Snapshot/string tests reject known pt-BR banned terms in rendered UI.

Manual checklist is documented in `docs/accessibility-manual-check.md` and covers screen-reader announcements, focus order, calendar fallback, error recovery and contrast. Automated results do not waive manual review.
