package handlers

import "testing"

func TestSafeCollectionReturn(t *testing.T) {
	memberID := "d4a3ea61-1176-4dc5-930e-b04ce610a1e7"
	tests := []struct{ raw, want string }{
		{"/admin/membros?q=Ana+%26+Jo%C3%A3o&page=2#member-" + memberID, "/admin/membros?page=2&q=Ana+%26+Jo%C3%A3o#member-" + memberID},
		{"/admin/membros?page=2#member-10000000-0000-0000-0000-000000000006", "/admin/membros?page=2#member-10000000-0000-0000-0000-000000000006"},
		{"/admin/membros?page=1", "/admin/membros"},
		{"/admin/fleet?repairs_page=2&equipment_page=3#repair-" + memberID, "/admin/fleet?equipment_page=3&repairs_page=2#repair-" + memberID},
		{"/admin/fleet#maintenance-schedule", "/admin/fleet#maintenance-schedule"},
		{"/admin/fleet#equipment-inventory", "/admin/fleet#equipment-inventory"},
		{"https://evil.test/admin/membros", ""},
		{"//evil.test/admin/membros", ""},
		{"/admin/membros?q=a&q=b", ""},
		{"/admin/membros?unknown=1", ""},
		{"/admin/membros?page=01", ""},
		{"/admin/membros?page=10001", ""},
		{"/admin/fleet?repairs_page=0", ""},
		{"/admin/fleet#repair-not-a-uuid", ""},
		{"/admin/fleet#repair-D4A3EA61-1176-4DC5-930E-B04CE610A1E7", ""},
		{"/admin/membros#equipment-" + memberID, ""},
		{"/admin/fleet#unknown-panel", ""},
	}
	for _, test := range tests {
		if got := safeCollectionReturn(test.raw); got != test.want {
			t.Errorf("safeCollectionReturn(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestAdminCollectionReturn(t *testing.T) {
	for _, tc := range []struct {
		raw, path, want string
	}{
		{"/admin/noticias?page=2", "/admin/noticias", "/admin/noticias?page=2"},
		{"/admin/albuns", "/admin/albuns", "/admin/albuns"},
		{"/admin/noticias?page=01", "/admin/noticias", ""},
		{"/admin/noticias?return_to=%2Fadmin%2Fnoticias", "/admin/noticias", ""},
		{"https://example.test/admin/noticias", "/admin/noticias", ""},
		{"/admin/fleet", "/admin/noticias", ""},
	} {
		if got := adminCollectionReturn(tc.raw, tc.path); got != tc.want {
			t.Errorf("adminCollectionReturn(%q, %q) = %q, want %q", tc.raw, tc.path, got, tc.want)
		}
	}
}
