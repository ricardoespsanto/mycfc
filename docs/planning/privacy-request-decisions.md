# Privacy requests: decision packet for #110

Date: 2026-09-07. Status: the human approved the first four operating choices below on 2026-09-07. Retention-matrix adoption, named reviewer grants, public activation and release remain separate. No GitHub update is implied.

## Outcome and current boundary

An adult can request and track erasure, and a specifically authorized reviewer can verify identity, resolve dependant relationships, and record an explained decision. The workflow must not claim data was erased before the separate #111 executor completes its required work.

Public legal pages and registration consent corrections have shipped. #109 remains open for retention-matrix sign-off. Existing account-to-dependant links do not establish verified legal representation. Existing credential versions provide the session-revocation foundation.

## Approved operating choices and remaining evidence

Approval covers explicit privacy-review capability, access preserved until actual closure execution, verified representation and no orphaned dependants, and 24 months of minimal closed-case evidence. It does not assign authority to any account or approve the wider retention matrix. The approval/evidence column below preserves the remaining operational requirements; the four product choices themselves need no further approval.

| Decision | Product choice | Remaining evidence |
|---|---|---|
| Reviewer authority | Introduce an explicit privacy-case capability, granted and revoked with an actor audit. General administrator access alone grants no case access. Prevent self-review and self-approval. | CFC designates the authorized reviewer(s) and grant authority; no account is granted access by this proposal. |
| Account access | Submission and approval preserve access. Only an explicit start-processing operation for account closure disables authentication and revokes sessions, atomically with accepting execution work. Category-only erasure preserves account access unless the approved category requires a specific restriction. | Approved; enforce atomically at the future executor boundary. |
| Minors and adult closure | Allow a request to be received through the existing relationship, but require case-specific verification of representation before a decision or disclosure. A conflict pauses dependent actions. Adult closure cannot proceed until every dependant has a verified transfer or separate approved resolution. | Policy approved; designate who validates representation and record evidence for each actual case. Do not treat a browser ID or existing guardian link as proof. |
| Minimum case evidence | Retain minimal reference, dates, decision/result codes and evidence expiry for 24 months after case closure; remove active subject/requester links when execution permits. Keep identifiable working records separately and only while necessary. A documented exception must have an owner, reason and expiry. | Duration approved as a product policy, not a claim about a legally required period. Working-record retention and individual exceptions remain separate. |
| Retention matrix | Review and adopt each applicable category/action/period in `docs/legal/matriz-conservacao.md`; capture the approved matrix version on every decision. Keep unresolved external contracts, sporting/insurance obligations and backup periods explicitly unresolved. | Category-level sign-off and the outstanding operational evidence. Approval of the four choices above does not approve this matrix. |

## Proposed story scope

Title: Submit, review and track a private erasure request.

Actors: adult data subject, dependant using an age-appropriate rights route, adult requesting for a named dependant, and explicitly authorized privacy reviewer.

- Add a profile entry point, recent-auth confirmation, structured scope selection, acknowledgement, opaque case reference, status and cancellation before processing.
- Add a restricted reviewer queue with identity/representation checks, dated decisions, bounded explanations, due-date handling and optimistic concurrency.
- Send acknowledgement and decision notifications through the existing typed outbox, without sensitive case details.
- Record transactional, metadata-only case history. Separate protected working explanations from immutable audit events.
- Define the executor handoff contract. Until #111 is available, an approved case clearly remains awaiting execution; no start-processing control may strand a disabled account without accepted execution work.

Excluded: actual cross-store deletion, provider revocation, object-version cleanup, backup restore safeguards, automatic representation verification, a general-purpose rights portal, and production permission grants. The first four belong to #111.

## Observable acceptance criteria

1. A recently authenticated adult submits a bounded request once despite retries and sees a dated acknowledgement and opaque reference.
2. Unrelated users and ordinary administrators cannot discover cases; an authorized reviewer cannot decide their own request.
3. Named-dependant requests remain unresolved until representation is verified; adult closure cannot orphan a dependant or remove the last active administrator.
4. Every transition rejects stale versions and invalid source states, and atomically appends metadata-only history.
5. Submission and approval preserve account access. Cancellation works before processing. Category-only requests do not silently close the account.
6. Approved decisions retain the exact policy/matrix version and required category actions. Unresolved retention or representation decisions prevent progression rather than being guessed.
7. No case displays completed erasure merely because a reviewer approved it; processing requires a compatible executor.
8. Case pages use no-store, CSRF protection and opaque references. Logs and audit history exclude identity values, medical data, object/provider identifiers and free-form explanations.
9. Notifications remain understandable if account access later ends and do not require login as the only way to obtain the final explanation.
10. Member and reviewer journeys provide pt-PT labels, empty/loading/error/success states, visible keyboard focus, accessible errors and mobile reflow using existing components. Detailed UX/design review follows the policy decisions.

## Delivery and evidence

Before exposing the workflow, approve the exact selectable category scopes from the adopted matrix and a schedule for identifiable working explanations distinct from minimal closed-case evidence. Name an alternate reviewer for conflicts. Preserve the original receipt date through identity verification; revoke representative case access when the underlying relationship or authority ends.

Public activation also requires a functioning #111 executor or a separately approved, evidenced manual fulfilment route. Do not accumulate live approved cases behind an unavailable executor. Preparation of #110 can proceed independently, but narrowing its currently recorded dependency on #109 requires an approved issue update.

Suggested existing label: enhancement. Keep #110 as the request/review story and #111 as execution. Do not close #109 until its remaining acceptance evidence exists.

Implementation sequence: explicit capability and additive case schema; member request/status flow; reviewer decisions and outbox; executor boundary and independent review. Update the complete schema baseline, add forward-only migrations and generate sqlc output. Reuse credential-version revocation rather than introducing a second session mechanism.

Verification for the full workflow: state/authorization/recent-auth tests; PostgreSQL transaction, duplicate and concurrent-decision tests; outbox privacy tests; Playwright/axe member, dependant and reviewer coverage; independent security and QA review. Run the repository verification gate before release handoff.

## Implemented workflow

The application now provides the member routes under `/perfil/privacidade` and capability-only reviewer routes under `/admin/privacidade`. Receipt, claim, identity and representation verification, deadline extension, exact category decisions, partial approval, refusal and cancellation are persisted with optimistic concurrency, metadata-only history and the policy snapshot adopted at intake. Account-closure approval enforces current dependant resolutions and the last-administrator safeguard. Approval remains open as “Aprovado — a aguardar execução”; no route starts erasure or disables the account.

Durable reviewer grants, immutable policy import and activation audit are operated through `cmd/server privacy`. Intake also requires recent password confirmation and the current credential version, with bounded actor and network throttling. Guardian access is recalculated from the current relationship and is redacted until case-specific authority is verified. Notifications use sealed recipient/contact payloads and generic SMTP content so delivery does not depend on a surviving account and does not expose the case in mail or operational logs.

Closed identifiable working material expires on the adopted working-record schedule, while minimal evidence expires 24 calendar months after closure. The explicit maintenance command preserves open awaiting-execution cases plus reviewer and activation audit. `docs/privacy-request-operations.md` records activation, handling, retention and rollback procedures.

The workflow remains disabled by default. Synthetic browser fixtures do not adopt production policy, appoint real reviewers or prove fulfilment readiness. Actual erasure, provider/object cleanup and deletion-ledger replay remain #111.

## Remaining external facts

The club must still confirm applicable retention exceptions and provider/recipient arrangements. #111 must implement and test object-version deletion, backup expiration and independent deletion-ledger replay before any complete-erasure claim. These are execution dependencies, not reasons to rebuild the already delivered public legal pages.
