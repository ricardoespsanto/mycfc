package handlers

import "net/http"

// ClassificationStylesheet is a same-origin stylesheet: the application CSP
// intentionally disallows inline styles on the restricted staff task.
func ClassificationStylesheet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(`*{box-sizing:border-box}body{font:1rem/1.5 system-ui;max-width:52rem;margin:auto;padding:1rem;overflow-wrap:anywhere}fieldset{margin:1rem 0;min-width:0}label,input,select,textarea{display:block}input,select,textarea{max-width:100%;min-height:2.5rem}select,textarea{width:100%}#age-reason-group{display:none}body:has(#category_id option:checked[data-mismatch="true"]) #age-reason-group{display:block}:focus-visible{outline:3px solid #005fcc;outline-offset:3px}button,a{min-height:2.5rem}li{margin:.5rem 0}.error{border:2px solid currentColor;padding:1rem}@media(forced-colors:active){.error{border:2px solid CanvasText}}`))
}
