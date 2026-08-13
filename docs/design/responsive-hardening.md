# Responsive, touch and reflow hardening

Issue #60 hardens the completed authenticated page families after #64. The
checks run through the deterministic #62 personas and feed defects back into
shared primitives rather than adding route-specific exceptions.

## Automated matrix

Each listed route/persona state runs in both the desktop and mobile screenshot
contexts. The contract resets the page through 320, 375 and 768 CSS pixels, an
effective 640 CSS-pixel viewport for 200% browser zoom from the agreed 1280
baseline, 200% root text enlargement, and a 375×400 compact-height scenario
representing an open software keyboard.

| Persona | Representative states |
| --- | --- |
| Member | Today, events, announcements, authenticated 403 and authenticated 404 |
| Tutor | Today and dependent management |
| Athlete | Today, competition workspace and training |
| Coach | Today, event authoring and training authoring |
| Administrator | Today, member directory/detail, news and fleet operations |
| Multi-capability | Today, leisure, competition and event authoring |

For every state, Playwright verifies:

- no document-level horizontal overflow at every agreed width and zoom;
- frequent controls and navigation expose at least a 44×44 CSS-pixel target;
- 200% text enlargement does not clip the document;
- the first main-content control remains visible when focused in a compact
  viewport;
- all native disclosures can be expanded together without overflow, clipped
  enlarged text or undersized revealed controls;
- serious/critical axe violations remain absent;
- screenshot state is restored after stress testing.

Tables retain their header/cell relationships inside named, keyboard-focusable
horizontal data regions. The audit does not convert them to ambiguous cards.

## Shared defects remediated

- Increased application navigation, account/logout, admin tab, pagination,
  inline record action and disclosure targets to 44 CSS pixels.
- Made checkbox and radio labels full-size touch targets, including nested
  fieldsets and membership rows.
- Removed the 40-pixel override from repeated training actions.
- Made form submit minimum widths container-aware so expanded long forms reflow
  under 200% text enlargement.
- Retained wrapping for long Portuguese labels, errors, names and statuses.

## Visual evidence

The before images are the accepted post-#64 state; the after images show the
same deterministic pages after #60 hardening.

| Family | Desktop before | Desktop after | Mobile before | Mobile after |
| --- | --- | --- | --- | --- |
| Member detail | [before](responsive-hardening-evidence/before/member-detail/desktop.png) | [after](responsive-hardening-evidence/after/member-detail/desktop.png) | [before](responsive-hardening-evidence/before/member-detail/mobile.png) | [after](responsive-hardening-evidence/after/member-detail/mobile.png) |
| Training | [before](responsive-hardening-evidence/before/training/desktop.png) | [after](responsive-hardening-evidence/after/training/desktop.png) | [before](responsive-hardening-evidence/before/training/mobile.png) | [after](responsive-hardening-evidence/after/training/mobile.png) |

## Commands

- `make ui-review-screenshots` runs the full six-persona visual and responsive
  matrix from a fresh deterministic database. The final acceptance run passed
  all six persona projects across the 22 representative route/persona states.
- `CI=true make test-e2e` retains the interactive application
  journeys.
- `go test ./...` and `make verify-foundation` retain the server and generated
  asset gates.
