package httpx

import "net/http"

func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func Redirect(w http.ResponseWriter, r *http.Request, location string, status int) {
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", location)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, location, status)
}
