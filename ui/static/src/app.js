function focusReturnedFeedback(root = document) {
  const target = root.querySelector?.(".error-summary, [role='status'][tabindex='-1'], [role='alert'][tabindex='-1']");
  if (target instanceof HTMLElement) {
    target.focus({ preventScroll: true });
    target.scrollIntoView({ block: "nearest" });
  }
}

focusReturnedFeedback();

document.addEventListener("htmx:afterSwap", (event) => {
  focusReturnedFeedback(event.detail?.target || document);
});

document.addEventListener("submit", (event) => {
  const form = event.target;
  if (form instanceof HTMLFormElement && form.matches("form[hx-post]")) {
    form.setAttribute("aria-busy", "true");
    if (!form.querySelector("[data-pending-status]")) {
      const pending = document.createElement("span");
      pending.className = "visually-hidden";
      pending.dataset.pendingStatus = "true";
      pending.setAttribute("role", "status");
      pending.textContent = "A processar pedido.";
      form.append(pending);
    }
  }
});

for (const eventName of ["htmx:responseError", "htmx:sendError"]) {
  document.addEventListener(eventName, (event) => {
    const form = event.detail?.elt?.closest?.("form[hx-post]");
    form?.removeAttribute("aria-busy");
    form?.querySelector("[data-pending-status]")?.remove();
  });
}

const dateFormatter = new Intl.DateTimeFormat("pt-PT", {
  dateStyle: "full",
  timeStyle: "short",
  timeZone: "Europe/Lisbon",
});

for (const calendar of document.querySelectorAll("[data-calendar]")) {
  const apiKey = calendar.dataset.calendarApiKey;
  let sources;
  try {
    sources = JSON.parse(calendar.dataset.calendarSources || "[]");
  } catch {
    sources = [];
  }

  if (!apiKey || sources.length === 0) {
    calendar.replaceChildren();
    continue;
  }

  const timeMin = new Date().toISOString();
  const requests = sources.map((source) => {
    const endpoint = new URL(`https://www.googleapis.com/calendar/v3/calendars/${encodeURIComponent(source)}/events`);
    endpoint.search = new URLSearchParams({ key: apiKey, singleEvents: "true", orderBy: "startTime", timeMin, maxResults: "50" });
    return fetch(endpoint).then((response) => response.ok ? response.json() : Promise.reject(new Error("calendar request failed")));
  });

  Promise.allSettled(requests).then((results) => {
    const events = results.flatMap((result) => result.status === "fulfilled" ? result.value.items || [] : []);
    calendar.replaceChildren();
    if (events.length > 0) {
      const list = document.createElement("ul");
      list.className = "calendar-events";
      for (const event of events.sort((a, b) => (a.start.dateTime || a.start.date).localeCompare(b.start.dateTime || b.start.date))) {
        const item = document.createElement("li");
        const link = document.createElement("a");
        link.href = event.htmlLink;
        link.textContent = event.summary || "Evento sem título";
        const when = document.createElement("span");
        when.textContent = ` - ${event.start.dateTime ? dateFormatter.format(new Date(event.start.dateTime)) : event.start.date}`;
        item.append(link, when);
        list.append(item);
      }
      calendar.append(list);
    } else {
      const message = document.createElement("p");
      message.textContent = "Não existem eventos futuros nos calendários selecionados.";
      calendar.append(message);
    }
    if (results.some((result) => result.status === "rejected")) {
      const warning = document.createElement("p");
      warning.className = "calendar-warning";
      warning.setAttribute("role", "alert");
      warning.textContent = "Não foi possível atualizar todos os calendários. Tente novamente mais tarde.";
      calendar.prepend(warning);
      console.warn("Calendar source request failed");
    }
  });
}
