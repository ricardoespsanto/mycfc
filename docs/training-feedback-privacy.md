# Training feedback privacy and lifecycle

Post-session feedback is athlete-authored personal data. MyCFC stores actual duration and distance separately from the immutable coach prescription, together with optional perceived effort (0–10), overall feeling (1–5), and a note of at most 500 characters.

The values are descriptive only. MyCFC does not turn them into a diagnosis, injury flag, readiness decision, future prescription, TRIMP value, or provider-native load. If session-RPE load is introduced later, it must be a separately named and versioned metric family.

## Visibility

- The athlete can create and correct only their own completed-session response.
- A currently named guardian can read a minor dependant's response but cannot create or alter it.
- A current coach can read responses only through an active programme or team grant matching the prescription scope.
- Administrators can read responses for support and club administration.
- Unrelated users receive the same not-found result whether or not a response exists.
- Free-text notes must not appear in aggregate workload dashboards, rankings, logs, analytics events, or notification previews.

Every authorized read is resolved from current relationships and grants. The response remains joined to the exact immutable prescription revision recorded when the outcome was first submitted.

## Correction, export, and erasure rules

Corrections require the current outcome version and atomically increment it; `updated_at` records the accepted correction time. Planned and actual values use distinct columns and labels.

The personal-data export contract must include the session and prescription identifiers, outcome, actual duration and distance, perceived effort, overall feeling, note, reported time, corrected time, and version. No computed medical or readiness interpretation is exported because none is stored.

Training outcomes reference the athlete with `ON DELETE CASCADE`, so the feedback fields are removed with the athlete's outcome records when the future approved erasure workflow deletes that identity. Prescription retention and any legally required club record must be resolved explicitly by that workflow before deleting a user; the current immutable-prescription foreign keys must never be bypassed ad hoc. Until issues #110 and #111 deliver the request/review and execution workflows, this document is the binding inventory and lifecycle rule rather than a claim that self-service export or erasure is already available.
