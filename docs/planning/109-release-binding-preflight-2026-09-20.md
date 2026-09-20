# #109 release-binding preflight

**Status:** preparation only, 20 September 2026. This record creates no release, deployment, infrastructure change or privacy activation.

The #109 adoption pack and exception register were committed after the latest production release, `v1.25.1`. They cannot truthfully be recorded as evidence for that older release.

When a release is authorized, bind this adoption package only after the reviewed #109 commit is on `main`, canonical CI succeeds, and a signed annotated semantic version is created for that exact commit. The production workflow then supplies the immutable image, schema-migration identity, publication manifest and deployment receipt. A new restricted manifest must record those outputs together with the provider-package and legal-document hashes.

The release binding is separate from activation. Privacy requests, deletion execution, provider deletion, restore replay and the privacy worker remain inactive until their distinct operational requirements are completed.
