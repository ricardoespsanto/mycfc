# Terminology

User-facing European Portuguese uses **tutor** (singular) and **tutores**
(plural) for the adult responsible for a dependent minor.

The English `guardian` terminology remains intentional in internal Go names,
database columns and enum values, SQL queries, form fields, routes such as
`/dashboard/guardian`, command-line flags, fixtures, and historical English
specification prose. These names are established technical identifiers and public
contracts; changing them would require a separate compatibility and data-migration
decision.
