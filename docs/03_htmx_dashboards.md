# Task: Generate templ Components & CSRF-Protected UI

Replace standard templates with `a-h/templ`.

1. **Base Layout**: Create `base.templ`. Include PicoCSS, HTMX, the `htmx-ext-response-targets` script, and FullCalendar.js. 
2. **Security**: Ensure every form rendered inside `templ` components natively includes a hidden input populated by the `gorilla/csrf` token provided in the HTTP request context.
3. **Dashboards**: Create `dashboard_competitor.templ`.
4. **Calendar Integration**: Define a `div` for FullCalendar. Configure the JS initialization to pull events from public Google Calendar feeds using the `@fullcalendar/google-calendar` plugin.
5. **WhatsApp Directory**: Fetch records from `WhatsAppGroups` and render them securely.
