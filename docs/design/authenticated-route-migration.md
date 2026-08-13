# Authenticated route migration inventory

Issue #64 tracks every authenticated route in the deterministic #62 matrix.
The approved River Clubhouse shell and Today slice are the quality baseline;
route ownership remains governed by the approved information architecture and
the #56 migration.

| Route family | Page pattern | Migration state | Evidence owner |
| --- | --- | --- | --- |
| `/today` | composed home | complete | #57 |
| `/events`, `/events/{id}` | activity collection, detail and staff authoring | complete | #64 activity PR |
| `/treinos` | dense activity collection and staff authoring | complete | #64 activity PR |
| `/announcements`, `/announcements/{id}` | sparse activity collection, detail and staff authoring | complete | #64 activity PR |
| `/dashboard/guardian` | tutor collection and contextual forms | complete | #64 programme PR |
| `/dashboard/{programme}` | programme workspace | complete | #64 programme PR |
| coach/moderator compatibility routes | contextual redirect or intentional placeholder | complete: coach redirect in #56; moderator intentionally retained by #54 decision | #56 / #64 |
| `/admin/membros`, `/admin/membros/{id}` | dense directory and detail | complete | #58 / #64 member detail PR |
| `/admin/noticias` | dense publishing workflow | complete | #64 admin operations PR |
| `/admin/fleet` | operational dashboard and forms | complete | #64 admin operations PR |
| authenticated system/error states | bounded system state | complete | #64 system states PR |

Each page-family slice uses the shared page header, module, record list, badge,
empty-state, action and interaction contracts. Routes are only marked complete
after desktop/mobile evidence plus baseline keyboard, 320 CSS pixel, 200% zoom
and browser verification exists.
