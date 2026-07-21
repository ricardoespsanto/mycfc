# Task: Generate Base Template and Dashboard Views

We are using Go templates and HTMX. 

1. Create a `base.html` template that includes the HTML boilerplate, loads PicoCSS via CDN, and loads HTMX. It should define a `{{ block content . }}{{ end }}` area.
2. Create `dashboard_competitor.html`. It should inherit from `base`.
3. Inside the competitor dashboard, create an HTMX-powered widget for the Newsfeed. 
   * It should have a button or trigger that uses `hx-get=/api/news?filter=polo` to fetch discipline-specific news and swap it into a `div` with `hx-target=#news-container`.

Please write the Go handler that serves this template and the corresponding HTML file.
