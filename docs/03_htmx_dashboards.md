# Task: Generate ALL templ Dashboards & Calendar Integration

1. **Base Layout**: `base.templ`. Include PicoCSS, HTMX, `response-targets`, and the hidden CSRF input block.
2. **Calendar**: Integrate `@fullcalendar/google-calendar`. Use the `GOOGLE_CALENDAR_API_KEY` passed from the backend configuration to authenticate the feeds. Enforce the calendar timezone to `Europe/Lisbon`.

**Generate the 4 Role Dashboards:**
* `dashboard_competitor.templ`: Calendar, Performance Metrics, Training Logs, WhatsApp links.
* `dashboard_leisure.templ`: Filtered Calendar (social/cleans), Newsfeed, Photo Gallery link.
* `dashboard_guardian.templ`: Shows the dependent's schedule. Includes a form (POST to `/guardian/add-dependent`) to register a minor.
* `dashboard_admin.templ`: Fleet inventory overview, pending repair requests, and maintenance scheduling.
