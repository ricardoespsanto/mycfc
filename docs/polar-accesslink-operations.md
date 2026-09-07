# Polar AccessLink operations

Production uses the fixed OAuth callback `https://mycfcoimbra.com/oauth/polar/callback`. The integration has no runtime feature flag: it is available to directly authenticated adult athletes with an active programme membership whenever valid production configuration is present.

## Credential rollout and rotation

1. Rotate a Polar client secret immediately if it has appeared in a screenshot, log, terminal output, issue, or message.
2. Put only the replacement client ID and secret into the sensitive Terraform inputs. Apply the reviewed production plan so Secrets Manager receives them.
3. Verify the old client secret is rejected before deployment approval.
4. Keep the current `ACTIVITY_CREDENTIAL_KEY_ID` and key in the keyring while existing envelopes use it. Add a new key ID, make it active, then re-seal stored credentials before removing the old key. Removing an in-use key forces affected athletes to reconnect.

Never record authorization codes, OAuth state, access tokens, client credentials, request headers, Polar response bodies, heart-rate values, or training-load values in logs.

## Failure handling

- `reauthorization_required` or `consent_required`: the athlete reconnects Polar.
- `rate_limited`: wait before retrying; do not loop manual sync requests.
- `provider_unavailable` or `invalid_provider_response`: check provider status and application error counts without inspecting health payloads.
- A disconnect always clears local credentials and cancels active sync work. If Polar deregistration fails, ask the athlete to revoke MyCFC in Polar Flow too.

Imported exercise, heart-rate-zone, Training Load Pro, and Cardio Load evidence is retained after disconnect. Until issue #111 implements self-service erasure, an erasure request requires an authorized operator to identify the MyCFC user, export the affected row counts for approval, and delete that user's `activity_load_observations` and `synced_activities` in one reviewed transaction. Do not delete the user account or unrelated training records as part of that operation.
