# Visual direction exploration (#63)

This design-only exploration compares two materially different directions against the running application captured by the review harness. It does not change production templates, styles, routes, or behavior.

## Current-state evidence

The baseline is functional and intentionally plain. Across desktop and mobile, four issues recur: weak hierarchy between identity, context, and page task; large amounts of undifferentiated white space; navigation that does not communicate a member's overlapping responsibilities; and state treatments that rely too heavily on surrounding copy.

| Area | Desktop | Mobile | Annotation |
| --- | --- | --- | --- |
| Today | [capture](current-state/desktop/today.png) | [capture](current-state/mobile/today.png) | Needs a clear daily priority, not a flat collection of links. |
| Competition | [capture](current-state/desktop/competition.png) | [capture](current-state/mobile/competition.png) | Programme identity and progress need stronger grouping. |
| Tutor | [capture](current-state/desktop/tutor.png) | [capture](current-state/mobile/tutor.png) | “Acting as tutor” must remain visible while switching minors. |
| Events | [capture](current-state/desktop/events.png) | [capture](current-state/mobile/events.png) | Date, response, capacity, and management actions need a shared scan line. |
| Training / form-heavy | [capture](current-state/desktop/training.png) | [capture](current-state/mobile/training.png) | Editing and reading compete; errors need placement next to the decision. |
| Members | [capture](current-state/desktop/members.png) | [capture](current-state/mobile/members.png) | Dense administration requires stronger columns and mobile prioritisation. |
| Fleet | [capture](current-state/desktop/fleet.png) | [capture](current-state/mobile/fleet.png) | Operational status should read before maintenance detail. |

## Concept A — River Clubhouse

[Open the interactive prototype](prototype/river-clubhouse.html) · [desktop evidence](prototype/screenshots/river-clubhouse-desktop.jpg) · [mobile evidence](prototype/screenshots/river-clubhouse-mobile.jpg)

A calm application shell inspired by river colours and a physical clubhouse noticeboard. A dark persistent rail carries identity and responsibilities; warm content surfaces separate immediate tasks from reference information. On narrow screens, the rail becomes a compact identity header and horizontally scrollable task navigation.

- Identity: deep navy, river teal, mint, warm paper; rounded but not playful.
- Hierarchy: page intent first, next action second, supporting metrics and lists third.
- Multi-role model: role chips remain adjacent to the signed-in person; tutor context has a second explicit banner.
- Dense screens: tables retain columns on desktop and intentionally scroll as a single comparison surface on mobile.
- Sparse screens: empty states explain both why content is absent and the next available action.
- States: `#states` covers primary, secondary, disabled, success, warning, error, invalid form, empty, and disclosure states.

## Concept B — Regatta Editorial

[Open the interactive prototype](prototype/regatta-editorial.html) · [desktop evidence](prototype/screenshots/regatta-editorial-desktop.jpg) · [mobile evidence](prototype/screenshots/regatta-editorial-mobile.jpg)

A club bulletin or regatta programme: horizontal masthead navigation, strong typographic contrast, squared controls, rules instead of cards, and a live operational ticker. It gives the club a more distinctive public identity and makes dense information feel intentional. On mobile, stories become a single reading column and the context rail follows the primary content.

- Identity: ink, signal red, warm newsprint, serif editorial voice paired with utilitarian sans-serif controls.
- Hierarchy: headlines express urgency; rules and columns create rhythm without elevated cards.
- Multi-role model: responsibility count stays in the masthead; page-specific responsibility appears as a kicker or context rail.
- Dense screens: especially strong for members and fleet, where table and bulletin metaphors align.
- Sparse screens: editorial framing can feel oversized when there is little content.
- States: `#states` exercises the same state inventory using hard-edged notices and labels.

## Comparison and recommendation

| Dimension | A · River Clubhouse | B · Regatta Editorial |
| --- | --- | --- |
| Daily task clarity | Strong: persistent shell and predictable cards | Strong: urgency-led headlines, but hierarchy varies by content |
| Club identity | Familiar product language with distinctive colour | Most expressive and memorable |
| Mixed responsibilities | Best: identity and roles stay visible | Adequate: compact masthead has less room for role detail |
| Dense administration | Good; tables remain conventional | Best; editorial rules and compact type support scanning |
| Mobile adaptation | Best; shell transforms without changing task order | Good; masthead and horizontal navigation consume more height |
| Implementation risk | Lower; maps cleanly to reusable application primitives | Higher; page-specific composition needs stricter editorial rules |
| Accessibility | Familiar landmarks, large targets, explicit focus | Strong contrast/focus, but large display hierarchy needs careful heading discipline |

Recommend **Concept A, River Clubhouse**, using Concept B's strong section rules and compact table treatment selectively on administration screens. Concept A better supports the core problem: one person moving between member, tutor, athlete, and staff responsibilities on both desktop and mobile. The tradeoff is a less singular visual identity; that can be recovered through the river palette, mark, copy tone, and occasional editorial separators without adopting Concept B's full layout system.

## Responsive and accessibility contract

- Primary actions remain reachable without hover; all prototype navigation and disclosures use native controls.
- Focus is clearly visible, independent of colour; forced-colour and reduced-motion preferences receive explicit treatment.
- Status always includes text, not colour alone. Errors sit beside their field or decision and use `aria-invalid` where demonstrated.
- At 680px (A) / 780px (B), multi-column content becomes a single task order. Horizontal navigation is intentionally scrollable; tables preserve comparison by horizontal scrolling instead of collapsing labels ambiguously.
- Long Portuguese names and event titles wrap. Minimum touch targets are approximately 40px; production primitives should standardise on 44px where space allows.
- Production implementation must verify contrast tokens, 200% zoom, keyboard order, screen-reader landmarks, loading states, and live server errors with automated axe coverage plus manual review.

## Implementation handoff

- **#58 tokens and primitives:** start with Concept A's colour, spacing, radius, typography, focus ring, button, badge, notice, field, card, table, empty-state, and disclosure contracts. Pull Concept B's rule-based section separator and compact administration table in as optional variants—not a second design system.
- **#55 application shell:** implement the desktop responsibility rail, sticky workspace header, skip link, active navigation state, account switch/context affordance, and the mobile identity/navigation transformation. Preserve server-rendered links; the prototype buttons only simulate page switching.
- **#57 Today page:** compose the next event, required actions, chronological agenda, and responsibility-aware shortcuts. The “next” feature should remain useful when absent by yielding space to required actions rather than showing a decorative empty card.

## Review questions

1. Does the River Clubhouse direction feel specific enough to MyCFC while remaining calm for daily use?
2. Should the Regatta Editorial treatment be retained as an administration/table variant, or avoided to keep one visual grammar?
3. Is the responsibility context sufficiently explicit for people who are simultaneously tutor, athlete, and staff?

After direction approval, #58 should establish tokens and primitives before #55 and #57 consume them.
