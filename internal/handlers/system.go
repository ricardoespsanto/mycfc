package handlers

import (
	"fmt"
	"html"
	"io"
	"net/http"

	"github.com/cfcoimbra/mycfc/internal/httpx"
)

type System struct{}

func (System) NotImplemented(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotImplemented)
	requestID := html.EscapeString(httpx.RequestID(r.Context()))
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="pt-PT">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Funcionalidade em implementação</title></head>
<body><main id="conteudo-principal"><h1>Funcionalidade em implementação</h1><p>Esta parte da aplicação ainda não foi concluída.</p><p>Referência do pedido: <code>%s</code></p></main></body>
</html>`, requestID)
}

func (System) NotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `<!doctype html><html lang="pt-PT"><title>Página não encontrada</title><main id="conteudo-principal"><h1>Página não encontrada</h1><p>Verifique o endereço e tente novamente.</p></main></html>`)
}
