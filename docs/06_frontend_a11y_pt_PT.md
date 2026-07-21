# Task: Accessibility, Mobile Responsiveness, and Localization

Refine all `templ` components for WCAG accessibility and mobile readiness.

1. **Localization**: Enforce `<html lang="pt-PT">`. All UI vocabulary must be European Portuguese (Painel, Reportar Avaria, Pagaias, Embarcações, Notícias).
2. **Accessibility**:
   * Explicit `<label>` associations for all inputs.
   * Use `aria-busy="true"` on HTMX submission buttons to indicate loading states.
   * Ensure 422 validation error messages use high-contrast red text (`var(--pico-color-red-500)`) and `role="alert"`.
   * Ensure hidden CSRF inputs do not break semantic layout or screen reader flows.
3. **Mobile-Friendly**: Wrap data tables in responsive `<figure>` tags with horizontal overflow scrolling to prevent viewport breaking.
