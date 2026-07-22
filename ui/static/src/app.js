const forms = document.querySelectorAll("form[hx-post]");

for (const form of forms) {
  form.addEventListener("submit", () => {
    form.setAttribute("aria-busy", "true");
  });
}
