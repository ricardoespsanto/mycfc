# Task: Frontend Accessibility, Mobile Responsiveness, and Localization 

Refine the base HTML templates and HTMX partials to ensure they are accessible, mobile-responsive, and localized.

1. **Localization**:
   - Ensure the root document uses `<html lang=pt-PT>`.
   - Translate all application vocabulary to European Portuguese:
     - Dashboard -> Painel
     - Report Repair -> Reportar Avaria
     - Paddles -> Pagaias
     - Boats -> Embarcações
     - Newsfeed -> Notícias

2. **Accessibility (WCAG)**:
   - Ensure all `<form>` elements have explicit `<label>` associations.
   - Add `aria-labels` to interactive elements and utilize `aria-busy=true` on HTMX submission buttons to indicate loading states to screen readers.
   - Use semantic PicoCSS structure (e.g., `<main class=container>`, `<article>`).
   - Implement PicoCSS's native dark/light mode toggle that respects the user's `prefers-color-scheme`.

3. **Mobile-Friendly**:
   - Ensure the navigation menu collapses or stacks correctly on smaller viewports.
   - Wrap data tables (like the fleet inventory or event calendar) in responsive containers (e.g., `<figure>` or a `div` with `overflow-auto`) so they scroll horizontally rather than breaking the viewport on mobile devices.
