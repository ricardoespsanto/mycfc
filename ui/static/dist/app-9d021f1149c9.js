const forms = document.querySelectorAll("form[hx-post]");

for (const form of forms) {
  form.addEventListener("submit", () => {
    form.setAttribute("aria-busy", "true");
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
