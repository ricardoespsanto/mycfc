# Task: Multipart Form Uploads & S3 Integration

Implement the `POST /repairs` handler and `repair_form.templ`.

1. **templ Component (`repair_form.templ`)**:
   * `hx-post="/repairs"`, `enctype="multipart/form-data"`.
   * **Idempotency:** Must include `hx-disabled-elt="find button"` to prevent double-submissions.
   * Includes hidden CSRF token and `<input type="file" name="photo">`.
2. **Go Handler (`HandlePostRepair`)**:
   * Parses multipart form.
   * If a photo exists, streams it to AWS S3 (`S3_BUCKET_NAME`) via AWS SDK v2 (`manager.Uploader`), generating a UUID object key.
   * Inserts the `RepairRequest` (with S3 URL and status 'Pendente') into PostgreSQL via `sqlc`.
   * Returns a 200 OK success partial, or a 422 error partial for validation failures.
