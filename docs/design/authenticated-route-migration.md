# Authenticated route migration inventory

Issue #64 tracks every authenticated route in the deterministic #62 matrix.
The approved River Clubhouse shell and Today slice are the quality baseline;
route ownership remains governed by the approved information architecture and
the #56 migration.

| Route family | Page pattern | Migration state | Evidence owner |
| --- | --- | --- | --- |
| `/today` | composed home | complete | #57 |
| `/events`, `/events/{id}` | activity collection, detail and staff authoring | activity slice | #64 activity PR |
| `/treinos` | dense activity collection and staff authoring | activity slice | #64 activity PR |
| `/announcements`, `/announcements/{id}` | sparse activity collection, detail and staff authoring | activity slice | #64 activity PR |
| `/dashboard/guardian` | tutor collection and contextual forms | planned tutor/programme slice | #64 |
| `/dashboard/{programme}` | programme workspace | planned tutor/programme slice | #64 |
| coach/moderator compatibility routes | contextual redirect or intentional placeholder | review after #56 lands | #56 / #64 |
| `/admin/membros`, `/admin/membros/{id}` | dense directory and detail | foundation complete; detail polish planned | #58 / #64 |
| `/admin/noticias` | dense publishing workflow | planned administration slice | #64 |
| `/admin/fleet` | operational dashboard and forms | planned administration slice | #64 |
| authenticated system/error states | bounded system state | planned final page-family slice | #64 |

Each page-family slice uses the shared page header, module, record list, badge,
empty-state, action and interaction contracts. Routes are only marked complete
after desktop/mobile evidence plus baseline keyboard, 320 CSS pixel, 200% zoom
and no-JavaScript verification exists.
