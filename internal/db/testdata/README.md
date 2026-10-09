# Immutable predecessor fixtures

These compressed SQL files are exact historical baseline bytes, not newly
maintained schemas. Integration tests verify their uncompressed SHA-256 before
installing them in an owned disposable database/cluster.

| Fixture | Git source | Uncompressed SHA-256 |
| --- | --- | --- |
| `dated-participation-predecessor-68ba5f1.sql.gz` | `68ba5f1:internal/db/schema.sql` | `5721695f5da09b3704d0be9e17957bcf25c51c366a6eb3a27c8f204ed78861b2` |
| `disposable-predecessor-v1.25.9.sql.gz` | `v1.25.9:internal/db/schema.sql` | `8b660c86b52fa287a9d41b47ca703bc0b8c0789d86ec40dedd0fe1a1673bc4b3` |

To reproduce either fixture, obtain the exact source using `git show`, then use
Python `gzip.compress(source_bytes, mtime=0)`. Do not regenerate these files
from the current baseline. Ledger inventories are installed separately by the
tests, matching the predecessor's actual included migration cutoff.

The dated-participation test creates a random database via `TEST_DATABASE_URL`
and removes it on cleanup. The disposable-release test starts its own PostgreSQL
container with a dynamic loopback port; its fixed `mycfc` database and production
role names never target the caller's cluster. All account credentials and data
in that fixture are synthetic and confined to the test-owned container.
