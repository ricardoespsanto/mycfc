package handlers

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

var fleetPanelFragments = map[string]bool{
	"repair-requests":      true,
	"maintenance-schedule": true,
	"equipment-inventory":  true,
}

// safeCollectionReturn accepts only the two collection URL shapes owned by
// the Members and Fleet workflows. Login redirect validation remains stricter
// and deliberately separate.
func safeCollectionReturn(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" {
		return ""
	}
	if parsed.Path != parsed.EscapedPath() {
		return ""
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return ""
	}
	canonical := url.Values{}
	switch parsed.Path {
	case "/admin/membros":
		if !copySingleCollectionValue(values, canonical, "q", false) || !copyCollectionPage(values, canonical, "page") || len(values) != 0 {
			return ""
		}
		if parsed.Fragment != "" && !collectionFragmentAllowed(parsed.Fragment, "member") {
			return ""
		}
	case "/admin/fleet":
		for _, key := range []string{"equipment_page", "repairs_page", "maintenance_page"} {
			if !copyCollectionPage(values, canonical, key) {
				return ""
			}
		}
		if len(values) != 0 || (parsed.Fragment != "" && !fleetPanelFragments[parsed.Fragment] && !collectionFragmentAllowed(parsed.Fragment, "equipment", "repair", "maintenance")) {
			return ""
		}
	default:
		return ""
	}
	result := parsed.Path
	if encoded := canonical.Encode(); encoded != "" {
		result += "?" + encoded
	}
	if parsed.Fragment != "" {
		result += "#" + parsed.Fragment
	}
	return result
}

func copySingleCollectionValue(source, destination url.Values, key string, trim bool) bool {
	values, present := source[key]
	if !present {
		return true
	}
	delete(source, key)
	if len(values) != 1 {
		return false
	}
	value := values[0]
	if trim {
		value = strings.TrimSpace(value)
	}
	if value != "" {
		destination.Set(key, value)
	}
	return true
}

func copyCollectionPage(source, destination url.Values, key string) bool {
	values, present := source[key]
	if !present {
		return true
	}
	delete(source, key)
	if len(values) != 1 {
		return false
	}
	page, err := strconv.Atoi(values[0])
	if err != nil || page < 1 || page > 10000 || strconv.Itoa(page) != values[0] {
		return false
	}
	if page > 1 {
		destination.Set(key, values[0])
	}
	return true
}

func collectionFragmentAllowed(fragment string, kinds ...string) bool {
	for _, kind := range kinds {
		prefix := kind + "-"
		if strings.HasPrefix(fragment, prefix) {
			rawID := strings.TrimPrefix(fragment, prefix)
			id, err := uuid.Parse(rawID)
			return err == nil && id.String() == rawID
		}
	}
	return false
}

func collectionReturnOr(raw, fallback string) string {
	if safe := safeCollectionReturn(raw); safe != "" {
		return safe
	}
	return fallback
}

// adminCollectionReturn accepts a bounded return location for a task that is
// owned by an administrator collection other than Members or Fleet. It keeps
// the same-origin and canonical-query guarantees as safeCollectionReturn
// without broadening that older helper's accepted surface.
func adminCollectionReturn(raw, path string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || parsed.Path != path || parsed.Path != parsed.EscapedPath() {
		return ""
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return ""
	}
	canonical := url.Values{}
	if !copyCollectionPage(values, canonical, "page") || len(values) != 0 {
		return ""
	}
	result := path
	if encoded := canonical.Encode(); encoded != "" {
		result += "?" + encoded
	}
	return result
}

func collectionURLWithFragment(base, fragment string) string {
	base = strings.SplitN(base, "#", 2)[0]
	return base + "#" + fragment
}
