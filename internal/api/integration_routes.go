package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fs0ciety7000/gonema/internal/mediaserver"
	"github.com/fs0ciety7000/gonema/internal/notify"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// RegisterIntegrationRoutes adds the notification and media server endpoints.
// Secrets are write-only: responses say whether one is stored (secretsSet,
// tokenSet, passwordSet), never its value. Either service may be nil. Call it
// once, after New and before serving.
func (s *Server) RegisterIntegrationRoutes(n *notify.Service, m *mediaserver.Service) {
	r := &integrationRoutes{s: s, n: n, m: m}
	if n != nil {
		r.notifications()
	}
	if m != nil {
		r.mediaServers()
	}
}

type integrationRoutes struct {
	s *Server
	n *notify.Service
	m *mediaserver.Service
}

// problem maps the integration errors; the others go through Server.problem.
func (r *integrationRoutes) problem(ctx context.Context, err error) error {
	msg := errText(err)
	switch {
	case msg != "" && errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict(msg)
	case msg != "" && (errors.Is(err, notify.ErrInvalid) || errors.Is(err, mediaserver.ErrInvalid)):
		return huma.Error422UnprocessableEntity(msg)
	case msg != "" && (errors.Is(err, notify.ErrNotFound) || errors.Is(err, mediaserver.ErrNotFound)):
		return huma.Error404NotFound(msg)
	}
	return r.s.problem(ctx, err, false)
}

// ----------------------------------------------------------------- notifications

type notificationBody struct {
	Body notify.NotificationInput
}

type notificationIDBody struct {
	IDParam
	Body notify.NotificationInput
}

type notificationTestOutput struct {
	Body notify.NotificationTest
}

// NotificationAppSettings are the settings shared by every notification.
type NotificationAppSettings struct {
	AppURL string `json:"appUrl" maxLength:"2048" doc:"Address Gonema is reached at (base path included); message links open movies there. Empty: no links."`
}

func (r *integrationRoutes) notifications() {
	n := r.n
	tags := []string{"Notifications"}
	huma.Register(r.s.api, huma.Operation{
		OperationID: "list-notifications", Method: http.MethodGet, Path: "/notifications", Summary: "List notifications",
		Description: "Secrets are never returned: secretsSet tells which are stored. eventMatrix lists every event with its state.", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []notify.Notification }, error) {
		list, err := n.List(ctx)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body []notify.Notification }{list}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "get-notification-settings", Method: http.MethodGet, Path: "/notifications/settings",
		Summary: "Shared notification settings", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body NotificationAppSettings }, error) {
		return &struct{ Body NotificationAppSettings }{NotificationAppSettings{AppURL: n.AppURL(ctx)}}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "put-notification-settings", Method: http.MethodPut, Path: "/notifications/settings",
		Summary: "Change the shared notification settings", Tags: tags,
	}, func(ctx context.Context, in *struct{ Body NotificationAppSettings }) (*struct{ Body NotificationAppSettings }, error) {
		u, err := n.SetAppURL(ctx, in.Body.AppURL)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body NotificationAppSettings }{NotificationAppSettings{AppURL: u}}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "get-notification", Method: http.MethodGet, Path: "/notifications/{id}", Summary: "Get a notification", Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body notify.Notification }, error) {
		v, err := n.Get(ctx, in.ID)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body notify.Notification }{v}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "create-notification", Method: http.MethodPost, Path: "/notifications", Summary: "Add a notification",
		Description: "Secrets are stored encrypted. Use POST /notifications/test first to send a test message.",
		Tags:        tags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *notificationBody) (*struct{ Body notify.Notification }, error) {
		v, err := n.Create(ctx, in.Body)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body notify.Notification }{v}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "update-notification", Method: http.MethodPut, Path: "/notifications/{id}", Summary: "Replace notification settings",
		Description: "Omitted secrets keep their stored value (unless the kind changes). The health is reset.", Tags: tags,
	}, func(ctx context.Context, in *notificationIDBody) (*struct{ Body notify.Notification }, error) {
		v, err := n.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body notify.Notification }{v}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "delete-notification", Method: http.MethodDelete, Path: "/notifications/{id}", Summary: "Remove a notification",
		Tags: tags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		if err := n.Delete(ctx, in.ID); err != nil {
			return nil, r.problem(ctx, err)
		}
		return nil, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "test-notification", Method: http.MethodPost, Path: "/notifications/{id}/test", Summary: "Send a test message",
		Description: "Always 200: ok and a plain-language message. A success resets the health.", Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*notificationTestOutput, error) {
		res, err := n.Test(ctx, in.ID)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &notificationTestOutput{res}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "test-notification-settings", Method: http.MethodPost, Path: "/notifications/test",
		Summary:     "Send a test message with unsaved settings",
		Description: "With ?id= and omitted secrets, the stored secrets of that notification are used (same kind only).", Tags: tags,
	}, func(ctx context.Context, in *struct {
		ID   int64 `query:"id" minimum:"0" doc:"Stored notification whose secrets fill the omitted ones"`
		Body notify.NotificationInput
	}) (*notificationTestOutput, error) {
		res, err := n.TestConfig(ctx, in.ID, in.Body)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &notificationTestOutput{res}, nil
	})
}

// ----------------------------------------------------------------- media servers

type mediaServerBody struct {
	Body mediaserver.MediaServerInput
}

type mediaServerIDBody struct {
	IDParam
	Body mediaserver.MediaServerInput
}

type mediaServerTestOutput struct {
	Body mediaserver.NotificationTest
}

func (r *integrationRoutes) mediaServers() {
	m := r.m
	tags := []string{"Media servers"}
	huma.Register(r.s.api, huma.Operation{
		OperationID: "list-media-servers", Method: http.MethodGet, Path: "/media-servers", Summary: "List media servers",
		Description: "Tokens and passwords are never returned: tokenSet and passwordSet tell whether one is stored.", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body []mediaserver.MediaServer }, error) {
		list, err := m.List(ctx)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body []mediaserver.MediaServer }{list}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "get-media-server", Method: http.MethodGet, Path: "/media-servers/{id}", Summary: "Get a media server", Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*struct{ Body mediaserver.MediaServer }, error) {
		v, err := m.Get(ctx, in.ID)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body mediaserver.MediaServer }{v}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "create-media-server", Method: http.MethodPost, Path: "/media-servers", Summary: "Add a media server",
		Description: "The token or password is stored encrypted. Use POST /media-servers/test first to check the settings.",
		Tags:        tags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *mediaServerBody) (*struct{ Body mediaserver.MediaServer }, error) {
		v, err := m.Create(ctx, in.Body)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body mediaserver.MediaServer }{v}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "update-media-server", Method: http.MethodPut, Path: "/media-servers/{id}", Summary: "Replace media server settings",
		Description: "Omit token or password to keep the stored one. The health is reset.", Tags: tags,
	}, func(ctx context.Context, in *mediaServerIDBody) (*struct{ Body mediaserver.MediaServer }, error) {
		v, err := m.Update(ctx, in.ID, in.Body)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &struct{ Body mediaserver.MediaServer }{v}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "delete-media-server", Method: http.MethodDelete, Path: "/media-servers/{id}", Summary: "Remove a media server",
		Tags: tags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *IDParam) (*struct{}, error) {
		if err := m.Delete(ctx, in.ID); err != nil {
			return nil, r.problem(ctx, err)
		}
		return nil, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "test-media-server", Method: http.MethodPost, Path: "/media-servers/{id}/test", Summary: "Test a saved media server",
		Description: "Always 200: ok and a plain-language message. A success resets the health.", Tags: tags,
	}, func(ctx context.Context, in *IDParam) (*mediaServerTestOutput, error) {
		res, err := m.Test(ctx, in.ID)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &mediaServerTestOutput{res}, nil
	})
	huma.Register(r.s.api, huma.Operation{
		OperationID: "test-media-server-settings", Method: http.MethodPost, Path: "/media-servers/test",
		Summary:     "Test media server settings before saving",
		Description: "With ?id= and no token or password, the stored ones of that server are used.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		ID   int64 `query:"id" minimum:"0" doc:"Stored media server whose secrets fill the omitted ones"`
		Body mediaserver.MediaServerInput
	}) (*mediaServerTestOutput, error) {
		res, err := m.TestConfig(ctx, in.ID, in.Body)
		if err != nil {
			return nil, r.problem(ctx, err)
		}
		return &mediaServerTestOutput{res}, nil
	})
}
