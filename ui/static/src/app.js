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
  const scope = navigation.closest("[data-tab-scope]") || document;
  navigation.querySelectorAll("a").forEach((candidate) => {
    const selected = candidate === link;
    candidate.setAttribute("aria-selected", String(selected));
    candidate.setAttribute("tabindex", selected ? "0" : "-1");
    if (selected) candidate.setAttribute("aria-current", "page");
    else candidate.removeAttribute("aria-current");
  });
  scope.querySelectorAll("[data-tab-panel]").forEach((panel) => {
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
  const queryLink = [...new window.URLSearchParams(window.location.search).keys()].some((key) => key.startsWith("routine_"))
    ? links.find((link) => link.hash === "#training-routines")
    : null;
  activateCollectionTab(hashLink || queryLink || links[0]);
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

let taskDialogOpener = null;

function openTaskDialog(dialog, opener) {
  if (!(dialog instanceof HTMLElement) || typeof dialog.showModal !== "function") return;
  taskDialogOpener = opener instanceof HTMLElement ? opener : null;
  dialog.showModal();
  const firstField = dialog.querySelector("input:not([type='hidden']), select, textarea");
  (firstField || dialog.querySelector("[data-dialog-close]"))?.focus();
}

function closeTaskDialog(dialog) {
  if (!(dialog instanceof HTMLElement) || typeof dialog.close !== "function") return;
  dialog.close();
  taskDialogOpener?.focus();
  taskDialogOpener = null;
}

document.addEventListener("click", (event) => {
  const opener = event.target.closest?.("[data-dialog-open]");
  if (opener instanceof HTMLButtonElement) {
    const dialog = document.getElementById(opener.dataset.dialogOpen || "");
    if (dialog instanceof HTMLElement && typeof dialog.showModal === "function") openTaskDialog(dialog, opener);
    return;
  }
  const closer = event.target.closest?.("[data-dialog-close]");
  if (closer instanceof HTMLButtonElement) closeTaskDialog(closer.closest("dialog"));
});

for (const dialog of document.querySelectorAll("dialog[data-task-dialog]")) {
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) closeTaskDialog(dialog);
  });
  dialog.addEventListener("close", () => {
    if (taskDialogOpener) {
      taskDialogOpener.focus();
      taskDialogOpener = null;
    }
  });
}

for (const card of document.querySelectorAll("[data-training-card]")) {
  const toggle = card.querySelector(":scope > .training-card__header [data-training-toggle]");
  if (!(toggle instanceof HTMLButtonElement)) continue;
  const content = document.getElementById(toggle.getAttribute("aria-controls") || "");
  if (!(content instanceof HTMLElement)) continue;
  const storageKey = `mycfc:training-card:${toggle.getAttribute("aria-controls")}`;
  const stored = window.sessionStorage.getItem(storageKey);
  let expanded = stored === null ? card.dataset.defaultOpen === "true" : stored === "open";
  const render = () => {
    toggle.setAttribute("aria-expanded", String(expanded));
    content.hidden = !expanded;
    const label = toggle.querySelector("[data-training-toggle-label]");
    if (label) label.textContent = expanded ? "Ocultar" : "Mostrar";
  };
  toggle.addEventListener("click", () => {
    expanded = !expanded;
    window.sessionStorage.setItem(storageKey, expanded ? "open" : "closed");
    render();
  });
  render();
}

for (const form of document.querySelectorAll("form")) {
  const resistance = form.querySelector("select[name='resistance_kind']");
  if (!(resistance instanceof HTMLElement) || resistance.tagName !== "SELECT") continue;
  const valueField = form.querySelector("[data-resistance-value]");
  const textField = form.querySelector("[data-resistance-text]");
  const syncResistanceFields = () => {
    const numeric = ["KILOGRAMS", "PERCENT_1RM", "RPE", "RIR"].includes(resistance.value);
    const descriptive = ["BAND", "COACH_INSTRUCTION"].includes(resistance.value);
    if (valueField instanceof HTMLElement) valueField.hidden = !numeric;
    if (textField instanceof HTMLElement) textField.hidden = !descriptive;
  };
  resistance.addEventListener("change", syncResistanceFields);
  syncResistanceFields();
}

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

const variationGroupSelect = document.querySelector("#variation-group-base");
if (variationGroupSelect instanceof HTMLElement && variationGroupSelect.matches("select")) {
  const syncVariationMembers = () => {
    const groupID = variationGroupSelect.value;
    document.querySelectorAll("[data-variation-member]").forEach((card) => {
      const visible = groupID !== "" && card.dataset.group === groupID;
      card.hidden = !visible;
      const checkbox = card.querySelector("input[type='checkbox']");
      if (checkbox instanceof HTMLElement && checkbox.matches("input[type='checkbox']")) {
        checkbox.disabled = !visible;
        if (!visible) checkbox.checked = false;
      }
    });
  };
  variationGroupSelect.addEventListener("change", syncVariationMembers);
  syncVariationMembers();
}

const variationPlanSelect = document.querySelector("#variation-plan");
const variationTargetSelect = document.querySelector("#variation-target");
const variationSubjectSelect = document.querySelector("#variation-subject");
if (variationPlanSelect instanceof HTMLElement && variationPlanSelect.matches("select") && variationTargetSelect instanceof HTMLElement && variationTargetSelect.matches("select") && variationSubjectSelect instanceof HTMLElement && variationSubjectSelect.matches("select")) {
  const filterVariationOptions = (select, attribute, expected) => {
    [...select.options].forEach((option, index) => {
      if (index === 0) return;
      const visible = expected !== "" && option.dataset[attribute] === expected;
      option.hidden = !visible;
      option.disabled = !visible;
    });
    if (select.selectedOptions[0]?.disabled) select.value = "";
  };
  const syncVariationScope = () => {
    const selectedPlan = variationPlanSelect.selectedOptions[0];
    filterVariationOptions(variationTargetSelect, "group", selectedPlan?.dataset.group || "");
    filterVariationOptions(variationSubjectSelect, "plan", variationPlanSelect.value);
  };
  variationPlanSelect.addEventListener("change", syncVariationScope);
  syncVariationScope();
}

const variationOperation = document.querySelector("#variation-operation");
if (variationOperation instanceof HTMLElement && variationOperation.matches("select")) {
  const form = variationOperation.closest("form");
  const patchFields = form?.querySelectorAll("input[name]:not([name='version']), select[name='modality'], textarea[name='instructions']") || [];
  const excludedNames = new Set(["plan_id", "target", "subject", "operation", "change_summary"]);
  const syncVariationOperation = () => {
    const omit = variationOperation.value === "OMIT";
    patchFields.forEach((field) => {
      if (!excludedNames.has(field.name)) field.disabled = omit;
    });
  };
  variationOperation.addEventListener("change", syncVariationOperation);
  syncVariationOperation();
}

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
