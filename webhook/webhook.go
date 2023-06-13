package webhook

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/opencontainers/go-digest"
)

const (
	PayloadActionPush       = "push"
	PayloadActionQuarantine = "quarantine"
)

// Payload is payload of ACR webhook request
// Reference: https://github.com/MicrosoftDocs/azure-docs/blob/main/articles/container-registry/container-registry-webhook-reference.md#push-event
type Payload struct {
	Id     string `json:"id"`
	Action string `json:"action"`
	Target Target `json:"target"`
}

type Target struct {
	MediaType  string        `json:"mediaType,omitempty"`
	Digest     digest.Digest `json:"digest"`
	Size       int64         `json:"size"`
	Repository string        `json:"repository"`
}

type WebhookHandler struct {
	Tasks chan<- Task
}

func (h *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	bytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "read request body: %+v", err)
		return
	}

	var payload Payload
	if err := json.Unmarshal(bytes, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "decode webhook payload: %+v", err)
		return
	}

	if payload.Action != PayloadActionQuarantine {
		fmt.Fprintf(w, "Not a quarantine action. Payload:\n%s\n", string(bytes))
		return
	}

	h.Tasks <- Task{
		Repository: payload.Target.Repository,
		Digest:     payload.Target.Digest,
	}
	fmt.Fprintf(w, "Enqueued scan task for %s@%s", payload.Target.Repository, payload.Target.Digest)
}
