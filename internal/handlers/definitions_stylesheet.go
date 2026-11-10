package handlers

import "net/http"

// DefinitionsStylesheet keeps the existing task styles compatible with the
// application's same-origin-only style policy.
func DefinitionsStylesheet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(definitionsCSS))
}

const definitionsCSS = `
*{box-sizing:border-box}body{font:1rem/1.5 system-ui;max-width:48rem;margin:auto;padding:1rem;overflow-wrap:anywhere}
label,input,select{display:block;max-width:100%}input,select{width:100%;min-height:2.75rem;margin:.3rem 0 1rem;font:inherit}
button{min-height:2.75rem;font:inherit;padding:.4rem 1rem}a,button,input,select{touch-action:manipulation}
:focus-visible,[tabindex="-1"]:focus{outline:3px solid #205493;outline-offset:3px}
.error{border:2px solid currentColor;padding:1rem;margin:1rem 0}[aria-invalid="true"]{border:2px solid #a52820}
@media(forced-colors:active){.error,[aria-invalid="true"]{border-color:CanvasText}:focus-visible,[tabindex="-1"]:focus{outline-color:Highlight}}
`
