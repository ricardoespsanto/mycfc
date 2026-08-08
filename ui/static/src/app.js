document.documentElement.classList.add("js");

function focusReturnedFeedback(root = document) {
  const target = root.querySelector?.(".error-summary, [role='status'][tabindex='-1'], [role='alert'][tabindex='-1']");
  if (target instanceof HTMLElement) {
    target.focus({ preventScroll: true });
    target.scrollIntoView({ block: "nearest" });
  }
}

focusReturnedFeedback();

const announcementPanel = document.querySelector("[data-announcement-panel]");
const announcementTriggers = [...document.querySelectorAll("[data-announcement-trigger]")];
let announcementPanelRequest = null;
let announcementPanelOpener = null;

function updateAnnouncementBadges(count) {
  const unread = Number.isFinite(count) ? Math.max(0, count) : 0;
  const label = unread === 0 ? "Avisos, sem avisos por ler" : unread === 1 ? "Avisos, 1 aviso por ler" : `Avisos, ${unread} avisos por ler`;
  for (const trigger of announcementTriggers) {
    trigger.setAttribute("aria-label", label);
    const badge = trigger.querySelector("[data-announcement-badge]");
    if (!(badge instanceof HTMLElement)) continue;
    badge.hidden = unread === 0;
    badge.textContent = unread > 99 ? "99+" : String(unread);
  }
}

function loadAnnouncementPanel() {
  if (!(announcementPanel instanceof HTMLElement)) return Promise.reject(new Error("announcement panel unavailable"));
  if (announcementPanel.dataset.loaded === "true") return Promise.resolve();
  if (announcementPanelRequest) return announcementPanelRequest;
  announcementPanelRequest = fetch("/announcements/panel", { headers: { "X-Requested-With": "announcement-panel" } })
    .then((response) => {
      if (!response.ok) throw new Error(`announcement panel returned ${response.status}`);
      return response.text();
    })
    .then((markup) => {
      announcementPanel.innerHTML = markup;
      announcementPanel.dataset.loaded = "true";
      const content = announcementPanel.querySelector("[data-announcement-count]");
      updateAnnouncementBadges(Number(content?.dataset.announcementCount || 0));
    })
    .catch((error) => {
      announcementPanelRequest = null;
      throw error;
    });
  return announcementPanelRequest;
}

function openAnnouncementPanel(trigger) {
  if (!(announcementPanel instanceof HTMLElement)) return;
  announcementPanelOpener = trigger instanceof HTMLElement ? trigger : null;
  document.querySelector(".mobile-app-menu[open]")?.removeAttribute("open");
  announcementPanel.hidden = false;
  document.body.classList.add("announcement-panel-open");
  for (const candidate of announcementTriggers) candidate.setAttribute("aria-expanded", "true");
  announcementPanel.querySelector("[data-announcement-close]")?.focus();
}

function closeAnnouncementPanel(restoreFocus = true) {
  if (!(announcementPanel instanceof HTMLElement) || announcementPanel.hidden) return;
  announcementPanel.hidden = true;
  document.body.classList.remove("announcement-panel-open");
  for (const trigger of announcementTriggers) trigger.setAttribute("aria-expanded", "false");
  if (restoreFocus) announcementPanelOpener?.focus();
  announcementPanelOpener = null;
}

if (announcementPanel && announcementTriggers.length > 0) {
  loadAnnouncementPanel().catch(() => {});
  document.addEventListener("click", async (event) => {
    const trigger = event.target.closest?.("[data-announcement-trigger]");
    if (trigger instanceof HTMLAnchorElement) {
      event.preventDefault();
      if (!announcementPanel.hidden) {
        closeAnnouncementPanel();
        return;
      }
      try {
        await loadAnnouncementPanel();
        openAnnouncementPanel(trigger);
      } catch {
        window.location.assign(trigger.href);
      }
      return;
    }
    if (event.target.closest?.("[data-announcement-close]")) {
      closeAnnouncementPanel();
      return;
    }
    if (!announcementPanel.hidden && !announcementPanel.contains(event.target)) closeAnnouncementPanel(false);
  });
  document.addEventListener("keydown", (event) => {
    if (announcementPanel.hidden) return;
    if (event.key === "Escape") {
      closeAnnouncementPanel();
      return;
    }
    if (event.key !== "Tab") return;
    const focusable = [...announcementPanel.querySelectorAll('a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])')]
      .filter((element) => element.getClientRects().length > 0);
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  });
}

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

for (const typeSelect of document.querySelectorAll("[data-event-type]")) {
  const form = typeSelect.closest("form");
  const documentFields = form?.querySelector("[data-competition-document]");
  if (!(documentFields instanceof HTMLDetailsElement)) continue;
  const syncCompetitionFields = () => {
    const competition = typeSelect.value === "COMPETITION";
    documentFields.hidden = !competition;
    if (!competition) documentFields.open = false;
  };
  typeSelect.addEventListener("change", syncCompetitionFields);
  syncCompetitionFields();
}

const initRepairDuplicateWarnings = (root = document) => {
  for (const select of root.querySelectorAll?.("select[name='equipment_id']") || []) {
    if (select.dataset.duplicateWarningReady === "true") continue;
    const warning = select.parentElement?.querySelector("[data-repair-duplicate-warning]");
    if (!(warning instanceof HTMLElement)) continue;
    const syncWarning = () => {
      const count = Number(select.selectedOptions[0]?.dataset.openRepairs || 0);
      warning.hidden = count === 0;
      warning.textContent = count === 1
        ? "Já existe uma avaria aberta para este equipamento. Confirme se é o mesmo problema antes de continuar."
        : count > 1
          ? `Já existem ${count} avarias abertas para este equipamento. Confirme se o problema já foi reportado.`
          : "";
    };
    select.dataset.duplicateWarningReady = "true";
    select.addEventListener("change", syncWarning);
    syncWarning();
  }
};

initRepairDuplicateWarnings();

document.addEventListener("htmx:afterSwap", (event) => {
	focusReturnedFeedback(event.detail?.target || document);
	initRepairDuplicateWarnings(event.detail?.target || document);
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
