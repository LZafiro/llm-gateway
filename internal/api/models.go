package api

import (
	"net/http"

	"github.com/LZafiro/llm-gateway/internal/router"
)

type ModelLister interface {
	Models() []router.Model
}

func handleModels(lister ModelLister, keys KeyLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, apiErr := authenticate(keys, r); apiErr != nil {
			writeError(w, apiErr)
			return
		}
		models := lister.Models()
		data := make([]modelJSON, 0, len(models))
		for _, m := range models {
			data = append(data, modelJSON{ID: m.ID, Object: "model", OwnedBy: m.OwnedBy})
		}
		writeJSON(w, http.StatusOK, modelList{Object: "list", Data: data})
	}
}
