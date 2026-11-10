package handlers

import (
	"errors"
	"github.com/cfcoimbra/mycfc/ui/pages"
)

// GET may use canonical readonly display. An authorized POST losing its source
// or original eligibility must instead keep visible copyable own raw/proof.
// Caller supplies a fresh scoped, server-built ReloadURL; scope denial is404
// before this helper and never echoes the retained draft.
func focusedEditRecoveryPage(page *pages.StructuredWorkoutTaskPage, err error) {
	page.Editing = true
	page.ReadOnly = false
	page.Reviewing = false
	page.Preview = nil
	page.PreviewURL = ""
	switch {
	case errors.Is(err, errFocusedEditSourceChanged):
		page.Message = "O treino foi alterado entretanto. Os valores introduzidos foram mantidos para copiar; não os aplique à versão atual sem a recarregar e rever."
	case errors.Is(err, errFocusedEditReadOnly):
		page.Message = "Este treino já não pode ser alterado. Os valores introduzidos foram mantidos para copiar; consulte o treino atual."
	default:
		page.Message = "Esta revisão já não pode ser utilizada. Os valores introduzidos foram mantidos para copiar; consulte e recarregue o treino atual antes de rever novas alterações."
	}
	if page.Errors == nil {
		page.Errors = focusedWorkoutErrors()
	}
	page.Errors.Add("form", page.Message)
}
