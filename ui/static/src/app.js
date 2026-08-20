document.documentElement.classList.add("js");

const mobileNavigation = document.querySelector("[data-mobile-navigation]");
const mobileNavigationTrigger = document.querySelector("[data-mobile-navigation-open]");
const mobileNavigationFallback = document.querySelector("[data-mobile-navigation-fallback]");
let mobileNavigationOpener = null;
let mobileNavigationHistoryOwned = false;
let mobileNavigationHistoryBackPending = false;
let pendingMobileNavigationClose = null;
let mobileLogoutPending = false;

function submitMobileLogout(button) {
  if (!(button instanceof HTMLButtonElement) || !(button.form instanceof HTMLFormElement)) return;
  if (mobileLogoutPending) return;
  mobileLogoutPending = true;
  const form = button.form;
  button.disabled = true;
  fetch(form.action, { method: form.method || "post", body: new FormData(form), credentials: "same-origin", redirect: "follow" })
    .then((response) => {
      if (!response.ok) throw new Error("logout failed");
      window.location.assign("/login");
    })
    .catch(() => {
      mobileLogoutPending = false;
      button.disabled = false;
      form.requestSubmit(button);
    });
}

function interceptMobileLogoutEnter(event) {
  if (event.key !== "Enter" || !isDialog(mobileNavigation) || !mobileNavigation.open) return;
  const logoutButton = [event.target, document.activeElement]
    .find((candidate) => candidate instanceof HTMLElement && candidate.matches("button.logout-button[type='submit']"));
  if (!(logoutButton instanceof HTMLButtonElement)) return;
  event.preventDefault();
  submitMobileLogout(logoutButton);
}

document.addEventListener("keydown", interceptMobileLogoutEnter, true);

function finishMobileNavigationClose(restoreFocus = true) {
  if (!isDialog(mobileNavigation)) return;
  if (mobileNavigation.open) mobileNavigation.close();
  document.body.classList.remove("mobile-navigation-open");
  mobileNavigationTrigger?.setAttribute("aria-expanded", "false");
  if (restoreFocus && mobileNavigationOpener?.isConnected) mobileNavigationOpener.focus();
  mobileNavigationOpener = null;
  mobileNavigationHistoryOwned = false;
}

function requestMobileNavigationClose({ restoreFocus = true, navigate = "", afterClose = null } = {}) {
  if (!isDialog(mobileNavigation) || !mobileNavigation.open) {
    if (navigate) window.location.assign(navigate);
    else afterClose?.();
    return;
  }
  if (mobileNavigationHistoryOwned) {
    pendingMobileNavigationClose = { restoreFocus, navigate, afterClose };
    mobileNavigationHistoryBackPending = true;
    window.history.back();
    return;
  }
  finishMobileNavigationClose(restoreFocus);
  if (navigate) window.location.assign(navigate);
  else afterClose?.();
}

function openMobileNavigation(opener) {
  if (!isDialog(mobileNavigation) || mobileNavigation.open) return;
  closeAnnouncementPanel(false);
  mobileNavigationOpener = opener instanceof HTMLElement ? opener : null;
  mobileNavigation.showModal();
  document.body.classList.add("mobile-navigation-open");
  mobileNavigationTrigger?.setAttribute("aria-expanded", "true");
  window.history.pushState({ mycfcNavigation: true }, "", window.location.href);
  mobileNavigationHistoryOwned = true;
  mobileNavigationHistoryBackPending = false;
}

if (isDialog(mobileNavigation) && mobileNavigationTrigger instanceof HTMLButtonElement && mobileNavigationFallback instanceof HTMLDetailsElement) {
  mobileNavigationFallback.hidden = true;
  mobileNavigationTrigger.hidden = false;
  mobileNavigationTrigger.addEventListener("click", () => openMobileNavigation(mobileNavigationTrigger));
  mobileNavigation.querySelector("[data-mobile-navigation-close]")?.addEventListener("click", () => requestMobileNavigationClose());
  mobileNavigation.addEventListener("cancel", (event) => {
    event.preventDefault();
    requestMobileNavigationClose();
  });
  mobileNavigation.addEventListener("click", (event) => {
    if (event.target === mobileNavigation) {
      requestMobileNavigationClose();
      return;
    }
    const route = event.target.closest?.(".site-nav a[href]");
    if (route instanceof HTMLAnchorElement) {
      event.preventDefault();
      requestMobileNavigationClose({ restoreFocus: false, navigate: route.href });
    }
  });
  mobileNavigation.addEventListener("keydown", (event) => containDialogFocus(mobileNavigation, event));
  mobileNavigation.addEventListener("keydown", (event) => {
    interceptMobileLogoutEnter(event);
  });

  const mobileNavigationViewport = window.matchMedia("(max-width: 48rem)");
  mobileNavigationViewport.addEventListener("change", (event) => {
    if (event.matches || !mobileNavigation.open) return;
    const shouldUnwindHistory = mobileNavigationHistoryOwned
      && !mobileNavigationHistoryBackPending
      && window.history.state?.mycfcNavigation === true;
    finishMobileNavigationClose(false);
    pendingMobileNavigationClose = null;
    if (shouldUnwindHistory) {
      mobileNavigationHistoryBackPending = true;
      window.history.back();
    }
  });
}

window.addEventListener("popstate", () => {
	const historyBackWasPending = mobileNavigationHistoryBackPending;
	mobileNavigationHistoryBackPending = false;
	if (!isDialog(mobileNavigation) || !mobileNavigation.open) {
		if (historyBackWasPending) pendingMobileNavigationClose = null;
		return;
	}
  const close = pendingMobileNavigationClose || { restoreFocus: true, navigate: "", afterClose: null };
  pendingMobileNavigationClose = null;
  finishMobileNavigationClose(close.restoreFocus);
  if (close.navigate) window.location.assign(close.navigate);
  else close.afterClose?.();
});

function focusReturnedFeedback(root = document) {
  const target = root.querySelector?.(".error-summary, [data-task-feedback], [role='status'][tabindex='-1'], [role='alert'][tabindex='-1']");
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
			const activate = async () => {
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
			};
			if (isDialog(mobileNavigation) && mobileNavigation.open) {
				requestMobileNavigationClose({ restoreFocus: false, afterClose: () => { void activate(); } });
				return;
			}
			await activate();
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

function collectionTabForHash(links, hash) {
  const direct = links.find((link) => link.hash === hash && document.querySelector(link.hash)?.matches("[data-tab-panel]"));
  if (direct) return direct;
  if (!hash.startsWith("#")) return null;

  let anchorID;
  try {
    anchorID = window.decodeURIComponent(hash.slice(1));
  } catch {
    return null;
  }
  const anchor = document.getElementById(anchorID);
  const panel = anchor?.closest("[data-tab-panel]");
  if (!(panel instanceof HTMLElement)) return null;
  return links.find((link) => link.hash === `#${panel.id}`) || null;
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
  const hashLink = collectionTabForHash(links, window.location.hash);
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

function rowActionItems(menu) {
  const panel = menu.querySelector("[data-row-action-menu-panel]");
  if (!(panel instanceof HTMLElement)) return [];
  return [...panel.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])')]
    .filter((element) => element.getClientRects().length > 0);
}

function positionRowActionMenu(menu) {
  const summary = menu.querySelector("summary");
  const panel = menu.querySelector("[data-row-action-menu-panel]");
  if (!(summary instanceof HTMLElement) || !(panel instanceof HTMLElement)) return;
  const trigger = summary.getBoundingClientRect();
  const width = Math.min(272, Math.max(208, window.innerWidth - 16));
  const left = Math.max(8, Math.min(window.innerWidth - width - 8, trigger.right - width));
  panel.style.setProperty("--row-action-inline-start", `${left}px`);
  panel.style.setProperty("--row-action-block-start", `${Math.min(window.innerHeight - 64, trigger.bottom + 6)}px`);
  panel.style.setProperty("--row-action-width", `${width}px`);
}

function closeRowActionMenu(menu, restoreFocus = false) {
  if (!(menu instanceof HTMLDetailsElement)) return;
  delete menu.dataset.opening;
  const summary = menu.querySelector("summary");
  const panel = menu.querySelector("[data-row-action-menu-panel]");
  if (panel instanceof HTMLElement && panel.matches(":popover-open")) panel.hidePopover();
  menu.open = false;
  summary?.setAttribute("aria-expanded", "false");
  if (restoreFocus) summary?.focus();
}

function openRowActionMenu(menu, focus = "") {
  if (!(menu instanceof HTMLDetailsElement)) return;
  document.querySelectorAll("details[data-row-action-menu][open]").forEach((candidate) => {
    if (candidate !== menu) closeRowActionMenu(candidate);
  });
  menu.dataset.opening = "true";
  window.requestAnimationFrame(() => {
    if (menu.dataset.opening !== "true") return;
    delete menu.dataset.opening;
    const summary = menu.querySelector("summary");
    const panel = menu.querySelector("[data-row-action-menu-panel]");
    menu.open = true;
    summary?.setAttribute("aria-expanded", "true");
    positionRowActionMenu(menu);
    if (panel instanceof HTMLElement && typeof panel.showPopover === "function") {
      panel.setAttribute("popover", "manual");
      if (!panel.matches(":popover-open")) panel.showPopover();
    }
    const items = rowActionItems(menu);
    if (focus === "first") items[0]?.focus();
    if (focus === "last") items.at(-1)?.focus();
  });
}

for (const menu of document.querySelectorAll("details[data-row-action-menu]")) {
  const summary = menu.querySelector("summary");
  const panel = menu.querySelector("[data-row-action-menu-panel]");
  summary?.setAttribute("aria-expanded", "false");
  summary?.addEventListener("click", (event) => {
    event.preventDefault();
    if (menu.open) closeRowActionMenu(menu, true);
    else openRowActionMenu(menu);
  });
  summary?.addEventListener("keydown", (event) => {
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    openRowActionMenu(menu, event.key === "ArrowUp" || event.key === "End" ? "last" : "first");
  });
  menu.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopImmediatePropagation();
      closeRowActionMenu(menu, true);
      return;
    }
    if (event.key === "Tab") {
      window.setTimeout(() => {
        if (menu.open && !menu.contains(document.activeElement)) closeRowActionMenu(menu);
      }, 0);
      return;
    }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    const items = rowActionItems(menu);
    const index = items.indexOf(document.activeElement);
    if (index < 0 || items.length === 0) return;
    event.preventDefault();
    let next = index;
    if (event.key === "ArrowDown") next = (index + 1) % items.length;
    if (event.key === "ArrowUp") next = (index - 1 + items.length) % items.length;
    if (event.key === "Home") next = 0;
    if (event.key === "End") next = items.length - 1;
    items[next].focus();
  });
  panel?.addEventListener("toggle", (event) => {
    if (event.newState === "closed" && menu.open) {
      menu.open = false;
      summary?.setAttribute("aria-expanded", "false");
    }
  });
}

document.addEventListener("click", (event) => {
  document.querySelectorAll("details[data-row-action-menu][open]").forEach((menu) => {
    if (!menu.contains(event.target)) closeRowActionMenu(menu);
  });
});

window.addEventListener("resize", () => document.querySelectorAll("details[data-row-action-menu][open]").forEach((menu) => closeRowActionMenu(menu)));
window.addEventListener("scroll", () => document.querySelectorAll("details[data-row-action-menu][open]").forEach((menu) => closeRowActionMenu(menu)), { passive: true });

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

const taskStates = new WeakMap();
let activeTaskDialog = null;
let pendingDiscard = null;

function isDialog(element) {
  return element instanceof HTMLElement && element.tagName === "DIALOG" && typeof element.showModal === "function";
}

function taskState(dialog) {
  let state = taskStates.get(dialog);
  if (!state) {
    state = { dirty: false, historyOwned: false, opener: null, restoringHistory: false };
    taskStates.set(dialog, state);
  }
  return state;
}

function taskFocusTarget(dialog) {
  for (const selector of [".error-summary", "[data-task-feedback]", "[data-task-initial-focus]", "input:not([type='hidden']):not([disabled])", "select:not([disabled])", "textarea:not([disabled])", "[data-task-close]", "[data-dialog-close]"]) {
    const target = dialog.querySelector(selector);
    if (target instanceof HTMLElement) return target;
  }
  return null;
}

function lockTaskDocument() {
  document.body.classList.add("task-surface-open");
}

function unlockTaskDocument() {
  if (!document.querySelector("dialog:modal")) document.body.classList.remove("task-surface-open");
}

function openTaskDialog(dialog, opener, updateHistory = true) {
  if (!isDialog(dialog) || dialog.open) return;
  const state = taskState(dialog);
  state.opener = opener instanceof HTMLElement ? opener : null;
  state.dirty = false;
  state.historyOwned = false;
  activeTaskDialog = dialog;
  dialog.showModal();
  lockTaskDocument();
  if (updateHistory && dialog.dataset.taskUrl) {
    window.history.pushState({ mycfcTask: dialog.id }, "", dialog.dataset.taskUrl);
    state.historyOwned = true;
  }
  window.setTimeout(() => taskFocusTarget(dialog)?.focus(), 0);
}

function finishTaskClose(dialog, restoreFocus = true) {
  if (!isDialog(dialog)) return;
  const state = taskState(dialog);
  const opener = restoreFocus && state.opener?.isConnected ? state.opener : null;
  if (dialog.open) dialog.close();
  state.dirty = false;
  state.historyOwned = false;
  activeTaskDialog = activeTaskDialog === dialog ? null : activeTaskDialog;
  unlockTaskDocument();
  // Chromium restores focus after the native close event. Deferring this keeps
  // the exact opener focused when a dialog is dismissed from a tabbed context.
  if (opener) window.setTimeout(() => opener.focus(), 0);
  state.opener = null;
}

function completeTaskClose(dialog) {
  const state = taskState(dialog);
  if (state.historyOwned) {
    state.restoringHistory = true;
    window.history.back();
  } else {
    finishTaskClose(dialog);
  }
}

function closeDiscardConfirmation(restoreTaskFocus = true) {
  const confirmation = document.querySelector("dialog[data-task-discard-confirmation]");
  if (isDialog(confirmation) && confirmation.open) confirmation.close();
  if (restoreTaskFocus && isDialog(pendingDiscard?.dialog)) {
    const { dialog, previousFocus } = pendingDiscard;
    const target = previousFocus instanceof HTMLElement && previousFocus.isConnected && dialog.contains(previousFocus)
      ? previousFocus
      : taskFocusTarget(dialog);
    target?.focus();
  }
  pendingDiscard = null;
}

function confirmTaskDiscard(dialog, onDiscard = () => completeTaskClose(dialog)) {
  const confirmation = document.querySelector("dialog[data-task-discard-confirmation]");
  if (!isDialog(confirmation)) return;
  const previousFocus = document.activeElement;
  pendingDiscard = { dialog, onDiscard, previousFocus };
  confirmation.showModal();
  confirmation.querySelector("[data-task-keep-editing]")?.focus();
}

function resetTaskForm(dialog) {
  const form = dialog.querySelector("form[data-task-form]");
  if (!(form instanceof HTMLFormElement)) return;
  form.reset();
  for (const password of form.querySelectorAll("input[type='password']")) password.value = "";
  syncTaskAccountFields(form);
}

function requestTaskClose(dialog) {
  if (!isDialog(dialog)) return;
  if (taskState(dialog).dirty) {
    confirmTaskDiscard(dialog);
    return;
  }
  completeTaskClose(dialog);
}

function containDialogFocus(dialog, event) {
  if (event.key !== "Tab") return;
  const modalDialogs = [...document.querySelectorAll("dialog:modal")];
  if (modalDialogs.at(-1) !== dialog) return;
  const focusable = [...dialog.querySelectorAll("a[href], button:not([disabled]), input:not([type='hidden']):not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])")]
    .filter((element) => element.getClientRects().length > 0);
  if (focusable.length === 0) {
    event.preventDefault();
    return;
  }
  if (focusable.length === 1) {
    event.preventDefault();
    focusable[0].focus();
    return;
  }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

document.addEventListener("click", (event) => {
  const opener = event.target.closest?.("[data-task-open], [data-dialog-open]");
  if (opener instanceof HTMLElement) {
    const dialogID = opener.dataset.taskOpen || opener.dataset.dialogOpen || "";
    const dialog = document.getElementById(dialogID);
    if (isDialog(dialog)) {
      event.preventDefault();
      openTaskDialog(dialog, opener);
    }
    return;
  }
  const closer = event.target.closest?.("[data-task-close], [data-dialog-close], [data-task-cancel]");
  const taskDialog = closer?.closest?.("dialog[data-task-surface], dialog[data-task-dialog]");
  if (isDialog(taskDialog)) {
    event.preventDefault();
    requestTaskClose(taskDialog);
    return;
  }
  if (event.target.closest?.("[data-task-keep-editing]")) {
    closeDiscardConfirmation();
    return;
  }
  if (event.target.closest?.("[data-task-confirm-discard]")) {
    const decision = pendingDiscard;
    closeDiscardConfirmation(false);
    if (isDialog(decision?.dialog)) {
      taskState(decision.dialog).dirty = false;
      resetTaskForm(decision.dialog);
      decision.onDiscard();
    }
  }
});

for (const dialog of document.querySelectorAll("dialog[data-task-surface], dialog[data-task-dialog]")) {
	// Keep task forms available to browsers without JavaScript. Once enhancement is
	// running they become real modal tasks, reopening their owning task on errors.
	const openOnLoad = dialog.hasAttribute("data-task-open-on-load");
	if (dialog.open) dialog.close();
	dialog.addEventListener("click", (event) => {
    if (event.target === dialog) requestTaskClose(dialog);
  });
  dialog.addEventListener("cancel", (event) => {
    event.preventDefault();
    requestTaskClose(dialog);
  });
  dialog.addEventListener("keydown", (event) => containDialogFocus(dialog, event));
  dialog.querySelector("form[data-task-form]")?.addEventListener("input", () => { taskState(dialog).dirty = true; });
  dialog.querySelector("form[data-task-form]")?.addEventListener("change", () => { taskState(dialog).dirty = true; });
	if (openOnLoad) openTaskDialog(dialog, null, false);
}

document.querySelector("dialog[data-task-discard-confirmation]")?.addEventListener("cancel", (event) => {
  event.preventDefault();
  closeDiscardConfirmation();
});
document.querySelector("dialog[data-task-discard-confirmation]")?.addEventListener("keydown", (event) => {
  containDialogFocus(event.currentTarget, event);
});

window.addEventListener("popstate", () => {
  if (!isDialog(activeTaskDialog) || !activeTaskDialog.open) return;
  const dialog = activeTaskDialog;
  const state = taskState(dialog);
  if (state.restoringHistory || !state.dirty) {
    state.restoringHistory = false;
    finishTaskClose(dialog);
    return;
  }
  window.history.pushState({ mycfcTask: dialog.id }, "", dialog.dataset.taskUrl || window.location.href);
  state.historyOwned = true;
  confirmTaskDiscard(dialog, () => {
    state.restoringHistory = true;
    window.history.back();
  });
});

function syncTaskAccountFields(form) {
  const selected = form?.querySelector("[data-account-type] input[type='radio']:checked")?.value;
  for (const group of form?.querySelectorAll("[data-account-fields]") || []) {
    const active = group.dataset.accountFields === selected;
    group.hidden = !active;
    for (const field of group.querySelectorAll("input, select, textarea")) field.disabled = !active;
  }
}

for (const form of document.querySelectorAll("form[data-task-form]:has([data-account-type])")) {
  for (const control of form.querySelectorAll("[data-account-type] input[type='radio']")) {
    control.addEventListener("change", () => syncTaskAccountFields(form));
  }
  syncTaskAccountFields(form);
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

const submittingTaskForms = new WeakSet();
const submittingLocalForms = new WeakSet();

function setPendingSubmitter(form, submitter) {
  if (!(submitter instanceof HTMLElement) || !submitter.matches("button, input")) return;
  submitter.dataset.idleLabel = submitter.textContent || submitter.value;
  if (submitter.tagName === "INPUT") submitter.value = "A processar…";
  else submitter.textContent = "A processar…";
  submitter.disabled = true;
  form.dataset.pendingSubmitter = "true";
}

function resetPendingForm(form) {
  if (!(form instanceof HTMLFormElement)) return;
  submittingTaskForms.delete(form);
  submittingLocalForms.delete(form);
  delete form.dataset.localSubmitting;
  form.removeAttribute("aria-busy");
  form.querySelector("[data-pending-status]")?.remove();
  for (const submitter of form.querySelectorAll("[data-idle-label], [data-task-submit][disabled]")) {
    const idleLabel = submitter.dataset.idleLabel;
    if (submitter.tagName === "INPUT") submitter.value = idleLabel || submitter.value;
    else if (idleLabel) submitter.textContent = idleLabel;
    delete submitter.dataset.idleLabel;
    submitter.disabled = false;
  }
  delete form.dataset.pendingSubmitter;
}

function showLocalMutationError(form, message) {
  resetPendingForm(form);
  let feedback = form.parentElement?.querySelector("[data-local-network-error]");
  if (!(feedback instanceof HTMLElement)) {
    feedback = document.createElement("p");
    feedback.dataset.localNetworkError = "true";
    feedback.className = "status-message";
    feedback.setAttribute("role", "alert");
    feedback.setAttribute("tabindex", "-1");
    form.before(feedback);
  }
  feedback.textContent = message;
  feedback.focus();
}

function clearLocalMutationFeedback(form) {
  const target = form.closest("[data-local-action]");
  if (!(target instanceof HTMLElement)) return;
  for (const feedback of target.querySelectorAll("[data-local-network-error], [data-local-response-feedback]")) {
    feedback.remove();
  }
}

async function submitLocalMutation(form) {
  const targetSelector = form.getAttribute("hx-target");
  const target = targetSelector ? document.querySelector(targetSelector) : null;
  try {
    const action = new window.URL(form.getAttribute("hx-post") || form.action, window.location.href);
    if (action.origin !== window.location.origin) {
      showLocalMutationError(form, "A ação não pôde ser validada. Atualize a página e tente novamente.");
      return;
    }
    const body = new window.URLSearchParams();
    for (const [key, value] of new FormData(form)) {
      if (typeof value === "string") body.append(key, value);
    }
    const response = await fetch(action, {
      method: "POST",
      body,
      headers: { "HX-Request": "true" },
      credentials: "same-origin",
    });
    const responseURL = new window.URL(response.url);
    if (response.redirected) {
      if (responseURL.origin === window.location.origin) window.location.assign(responseURL.href);
      else showLocalMutationError(form, "A sessão mudou. Atualize a página e tente novamente.");
      return;
    }
    const contentType = response.headers.get("Content-Type") || "";
    const html = await response.text();
    if (responseURL.origin !== window.location.origin || responseURL.pathname !== action.pathname || !contentType.toLowerCase().includes("text/html") || ![200, 409, 422].includes(response.status) || /<!doctype|<html[\s>]|<body[\s>]/i.test(html)) {
      showLocalMutationError(form, "A resposta não pôde ser aplicada. Atualize a página e tente novamente.");
      return;
    }
    const template = document.createElement("template");
    template.innerHTML = html;
    const primary = template.content.firstElementChild;
    if (!(target instanceof HTMLElement) || !(primary instanceof HTMLElement) || primary.id !== target.id || !primary.matches("[data-local-action]")) {
      showLocalMutationError(form, "A resposta não pôde ser aplicada. Atualize a página e tente novamente.");
      return;
    }
    const expectedStatusID = target.id.replace("-action-", "-status-");
    for (const replacement of template.content.querySelectorAll("[hx-swap-oob]")) {
      if (replacement.id !== expectedStatusID || !replacement.matches("[data-local-status]")) continue;
      const current = document.getElementById(expectedStatusID);
      replacement.removeAttribute("hx-swap-oob");
      if (current) current.replaceWith(replacement);
    }
    if (response.ok) {
      target.replaceWith(primary);
      const feedback = primary.querySelector('[role="alert"], [role="status"]');
      if (feedback instanceof HTMLElement) feedback.focus();
    } else {
      target.querySelector("[data-local-response-feedback]")?.remove();
      const responseFeedback = primary.querySelector("[data-local-response-feedback]");
      const responseAlert = responseFeedback?.querySelector('[role="alert"]');
      if (!(responseFeedback instanceof HTMLElement) || !(responseAlert instanceof HTMLElement)) {
        showLocalMutationError(form, "A resposta não pôde ser aplicada. Atualize a página e tente novamente.");
        return;
      }
      target.prepend(responseFeedback);
      resetPendingForm(form);
      responseAlert.focus();
    }
  } catch {
    showLocalMutationError(form, "Não foi possível concluir a ação. Tente novamente.");
  }
}

document.addEventListener("submit", (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement)) return;
  if (form.matches("form[data-task-form]")) {
    if (submittingTaskForms.has(form)) {
      event.preventDefault();
      return;
    }
    submittingTaskForms.add(form);
    taskState(form.closest("dialog") || form).dirty = false;
    form.setAttribute("aria-busy", "true");
    setPendingSubmitter(form, event.submitter);
  }
  if (form.matches("form[data-local-mutation]")) {
    if (submittingLocalForms.has(form) || form.dataset.localSubmitting === "true") {
      event.preventDefault();
      return;
    }
    event.preventDefault();
    clearLocalMutationFeedback(form);
    submittingLocalForms.add(form);
    form.dataset.localSubmitting = "true";
    setPendingSubmitter(form, event.submitter);
    form.setAttribute("aria-busy", "true");
    void submitLocalMutation(form);
  }
  if (form.matches("form[hx-post], form[data-task-form]")) {
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
    resetPendingForm(form);
  });
}

window.addEventListener("pageshow", () => {
	for (const form of document.querySelectorAll("form[data-task-form], form[data-local-mutation]")) {
    for (const password of form.querySelectorAll("input[type='password']")) password.value = "";
		if (form.matches("form[data-task-form]")) syncTaskAccountFields(form);
    if (form.getAttribute("aria-busy") !== "true") continue;
		resetPendingForm(form);
  }
});
