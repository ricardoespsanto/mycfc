package pages

import "testing"

func TestProfileCountriesContainsCompleteUniqueISOList(t *testing.T) {
	countries := profileCountries()
	if len(countries) != 249 {
		t.Fatalf("country count = %d, want 249", len(countries))
	}
	if countries[0].Code != "PT" || countries[0].Label != "Portugal" {
		t.Fatalf("first country = %+v, want Portugal", countries[0])
	}
	seen := make(map[string]bool, len(countries))
	for _, country := range countries {
		if len(country.Code) != 2 || country.Label == "" || seen[country.Code] {
			t.Fatalf("invalid or duplicate country: %+v", country)
		}
		seen[country.Code] = true
	}
}
