# Polar Flow setup

The Polar Flow connection is optional and is off until all settings below exist. The club must first register a Polar AccessLink application and set its redirect address to:

```text
https://mycfcoimbra.com/oauth/polar/callback
```

Store these values in the production runtime configuration; do not commit them or put them in issue comments:

- `POLAR_CLIENT_ID` — Polar's application identifier.
- `POLAR_CLIENT_SECRET` — Polar's application secret.
- `ACTIVITY_CREDENTIAL_KEY_ID` — the active key identifier.
- `ACTIVITY_CREDENTIAL_KEYS_JSON` — the existing JSON key ring, containing Base64-encoded 32-byte keys used only for member Polar credentials.

The production values are held in the existing AWS runtime parameter and application-secret stores. Before the next Terraform plan, copy the exact existing values into `polar_client_id`, `polar_client_secret`, `activity_credential_key_id`, and `activity_credential_keys_json` in **both** protected production plan and apply `TF_VARS`. They are required inputs so a missing value stops the plan instead of silently dropping Polar access or the existing credential key ring during the v2 secret cutover. If Polar has never been configured, explicitly set all four to empty strings in both protected inputs.

Each member authorizes Polar in their own browser. The application stores the provider identity, consent scope, and encrypted credential; it reads the seven most recent days only when the member completes the connection or selects **Sincronizar agora**. Disconnect removes the local credentials immediately. Polar's API does not provide a token revocation operation, so local destruction is the effective disconnect.

Before enabling production, add the factual Polar provider record and data-retention details required by the club's approved privacy process, then validate a real account connection and disconnect.

AccessLink webhooks are not enabled in this slice because the existing activity foundation has no provider webhook delivery worker or endpoint. A later follow-up can add verified webhook delivery after the club has a working provider connection; the member-controlled login and manual sync cover the initial release.
