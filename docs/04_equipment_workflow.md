# Task: Multipart Form Uploads & S3 Integration

Implement the "Report Boat Repair" flow allowing users to upload photos of broken equipment directly to S3.

1. **templ Component**: Create `repair_form.templ`.
   * Use `hx-ext="response-targets"`, `hx-post="/repairs"`, and `enctype="multipart/form-data"`.
   * Include `<input type="file" name="photo" accept="image/*">` and the hidden CSRF token.
   * Target `#repair-status` for 200 OK, and `#form-error` for 422 failures.
2. **Go Handler**: Write `HandlePostRepair`.
   * Parse the multipart form. Extract the image buffer.
   * If a photo exists, use the AWS SDK for Go v2 to stream the upload to the S3 bucket defined in the config. Set the object key as a generated UUID.
   * Save the `RepairRequest` to PostgreSQL via `sqlc`, including the returned S3 URL. Return a success `templ` partial.
