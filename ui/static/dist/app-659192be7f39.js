document.documentElement.classList.add("js");

function focusReturnedFeedback(root = document) {
  const target = root.querySelector?.(".error-summary, [role='status'][tabindex='-1'], [role='alert'][tabindex='-1']");
  if (target instanceof HTMLElement) {
    target.focus({ preventScroll: true });
    target.scrollIntoView({ block: "nearest" });
  }
}

focusReturnedFeedback();

let createPanelOpener = null;

function openCreatePanel(panel, opener) {
  if (!(panel instanceof HTMLDetailsElement)) return;
  document.querySelectorAll("details[data-create-panel][open]").forEach((candidate) => {
    if (candidate !== panel) candidate.open = false;
  });
  createPanelOpener = opener instanceof HTMLElement ? opener : null;
  panel.open = true;
  opener?.setAttribute?.("aria-expanded", "true");
  panel.querySelector(".create-panel__close")?.focus();
}

function closeCreatePanel(panel, restoreFocus = true) {
  if (!(panel instanceof HTMLDetailsElement)) return;
  panel.open = false;
  createPanelOpener?.setAttribute?.("aria-expanded", "false");
  if (restoreFocus) createPanelOpener?.focus();
  createPanelOpener = null;
}

function activateCollectionTab(link) {
  const navigation = link.closest("[data-collection-tabs]");
  const target = document.querySelector(link.hash);
  if (!navigation || !(target instanceof HTMLElement) || !target.matches("[data-tab-panel]")) return;
  navigation.querySelectorAll("a").forEach((candidate) => {
    const selected = candidate === link;
    candidate.setAttribute("aria-selected", String(selected));
    candidate.setAttribute("tabindex", selected ? "0" : "-1");
    if (selected) candidate.setAttribute("aria-current", "page");
    else candidate.removeAttribute("aria-current");
  });
  document.querySelectorAll("[data-tab-panel]").forEach((panel) => {
    panel.hidden = panel !== target;
  });
}

for (const navigation of document.querySelectorAll("[data-collection-tabs]")) {
  navigation.setAttribute("role", "tablist");
  const links = [...navigation.querySelectorAll("a")];
  for (const link of links) {
    link.setAttribute("role", "tab");
    const panel = document.querySelector(link.hash);
    if (panel instanceof HTMLElement) {
      if (!link.id) link.id = `${panel.id}-tab`;
      link.setAttribute("aria-controls", panel.id);
      panel.setAttribute("role", "tabpanel");
      panel.setAttribute("aria-labelledby", link.id);
    }
  }
  const hashLink = links.find((link) => link.hash === window.location.hash && document.querySelector(link.hash)?.matches("[data-tab-panel]"));
  activateCollectionTab(hashLink || links[0]);
  navigation.addEventListener("keydown", (event) => {
    const currentIndex = links.indexOf(document.activeElement);
    if (currentIndex < 0 || !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    let nextIndex = currentIndex;
    if (event.key === "ArrowLeft") nextIndex = (currentIndex - 1 + links.length) % links.length;
    if (event.key === "ArrowRight") nextIndex = (currentIndex + 1) % links.length;
    if (event.key === "Home") nextIndex = 0;
    if (event.key === "End") nextIndex = links.length - 1;
    activateCollectionTab(links[nextIndex]);
    links[nextIndex].focus();
  });
}

for (const link of document.querySelectorAll('a[href^="#"]')) {
  const panel = document.querySelector(link.hash);
  if (panel instanceof HTMLDetailsElement) link.setAttribute("aria-expanded", String(panel.open));
}

document.addEventListener("click", (event) => {
  const createLink = event.target.closest?.('a[href^="#"]');
  if (createLink instanceof HTMLAnchorElement) {
    const panel = document.querySelector(createLink.hash);
    if (panel?.matches?.("[data-create-panel]")) {
      event.preventDefault();
      openCreatePanel(panel, createLink);
      return;
    }
    if (panel instanceof HTMLDetailsElement) {
      event.preventDefault();
      panel.open = true;
      createLink.setAttribute("aria-expanded", "true");
      history.replaceState(null, "", createLink.hash);
      panel.scrollIntoView({ block: "start" });
      panel.querySelector("summary")?.focus();
      return;
    }
    if (createLink.closest("[data-collection-tabs]") && panel?.matches?.("[data-tab-panel]")) {
      event.preventDefault();
      activateCollectionTab(createLink);
      history.replaceState(null, "", createLink.hash);
      return;
    }
  }
  const closeButton = event.target.closest?.(".create-panel__close");
  if (closeButton instanceof HTMLButtonElement) closeCreatePanel(closeButton.closest("details[data-create-panel]"));
});

document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  const panel = document.querySelector("details[data-create-panel][open]");
  if (panel) closeCreatePanel(panel);
});

const initialCreatePanel = window.location.hash ? document.querySelector(window.location.hash) : null;
if (initialCreatePanel?.matches?.("[data-create-panel]")) openCreatePanel(initialCreatePanel, null);

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
