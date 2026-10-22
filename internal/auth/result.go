package auth

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/codetreker/uniauth/internal/apps"
)

type RetrievedResult struct {
	Identity    json.RawMessage
	OperationID string
	Provider    string
}

// ConsumeResult uses live application credentials and the same atomic claim as
// HTTP retrieval. Successful consumption transfers ownership of the payload.
func (h *Handler) ConsumeResult(ctx context.Context, appID, secret, code string) (RetrievedResult, error) {
	if ctx == nil {
		return RetrievedResult{}, ErrInvalidRequest
	}
	if h.ctx.Err() != nil {
		return RetrievedResult{}, ErrClosed
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	defer cancel()
	app, err := h.authenticateApp(ctx, appID, secret)
	if err != nil {
		if h.ctx.Err() != nil {
			return RetrievedResult{}, ErrClosed
		}
		return RetrievedResult{}, err
	}
	entry, err := h.claimResult(ctx, app.ID, code)
	if err != nil {
		return RetrievedResult{}, err
	}
	metadata := requestMetadata{requestID: token(16), operationID: entry.OperationID, appID: app.ID, provider: entry.Provider}
	h.logContext(context.WithValue(ctx, metadataKey{}, metadata), "result_consume", "")
	return entry, nil
}

func (h *Handler) authenticateApp(ctx context.Context, appID, secret string) (apps.App, error) {
	if len(appID) > 128 || len(secret) > 512 || appID == "" || secret == "" {
		return apps.App{}, apps.ErrUnauthorized
	}
	return h.apps.Authenticate(ctx, appID, secret)
}

func (h *Handler) claimResult(ctx context.Context, appID, code string) (RetrievedResult, error) {
	if !validResultCode(code) {
		return RetrievedResult{}, ErrInvalidRequest
	}
	if h.ctx.Err() != nil {
		return RetrievedResult{}, ErrClosed
	}
	entry, err := h.cache.takeResult(ctx, code, appID, h.config.Now())
	if err != nil {
		return RetrievedResult{}, err
	}
	return RetrievedResult{Identity: entry.payload, OperationID: entry.operationID, Provider: entry.provider}, nil
}

func validResultCode(code string) bool {
	return code != "" && len(code) <= 512 && utf8.ValidString(code)
}
