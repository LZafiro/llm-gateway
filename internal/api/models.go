package api

import (
	"net/http"

	"github.com/LZafiro/llm-gateway/internal/router"
)

type ModelLister interface {
	Models() []router.Model
}

func handleModels(lister ModelLister) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		models := lister.Models()
		data := make([]modelJSON, 0, len(models))
		for _, m := range models {
			data = append(data, modelJSON{ID: m.ID, Object: "model", OwnedBy: m.OwnedBy})
		}
		writeJSON(w, http.StatusOK, modelList{Object: "list", Data: data})
	}
}
