# Task: Equipment Repair HTMX Workflow

I need to implement the Report Boat Repair flow for Club Admins and Athletes.

1. Create an HTML form snippet (`repair_form.html`) that uses HTMX. 
   * Fields: Equipment Dropdown (populated from DB), Issue Description.
   * Action: `hx-post=/repairs`
   * Target: `hx-target=#repair-status` to show a success message without reloading.
2. Write the Go handler `HandlePostRepair(w http.ResponseWriter, r *http.Request)` that parses this form, saves the `RepairRequest` to the database, and returns a small HTML snippet (e.g., `<div class=success>Repair logged successfully!</div>`) to be swapped into the DOM.
