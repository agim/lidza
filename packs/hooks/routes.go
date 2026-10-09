package hooks

import (
	"context"
	"net/http"

	"github.com/agim/lidza/packs/auth"
	"github.com/agim/lidza/pkg/router"
)

// Options configure the endpoint routes.
type Options struct {
	// Owner names the owner of the request's endpoints: by default the
	// request's workspace (auth.WorkspaceID, behind
	// auth.RequireWorkspace) when there is one, else the signed-in user.
	Owner func(ctx context.Context) string
	// Guard protects the routes: auth.Require() by default. An app whose
	// endpoints belong to workspaces passes auth.RequireWorkspace(), or a
	// permission check (roles.Require("hooks.manage", ...)).
	Guard func(http.Handler) http.Handler
}

// Created is what creating an endpoint returns: the endpoint and its
// secret, shown once.
type Created struct {
	Endpoint Endpoint `json:"endpoint"`
	Secret   string   `json:"secret"`
}

// SecretReply carries an endpoint's secret.
type SecretReply struct {
	Secret string `json:"secret"`
}

// UpdateInput changes an endpoint; Enable turns a disabled one back on.
type UpdateInput struct {
	EndpointInput
	Enable bool `json:"enable"`
}

type routes struct{ opt Options }

func (rt routes) owner(ctx context.Context) string {
	if rt.opt.Owner != nil {
		return rt.opt.Owner(ctx)
	}
	if ws := auth.WorkspaceID(ctx); ws != "" {
		return ws
	}
	if u := auth.CurrentUser(ctx); u != nil {
		return u.ID
	}
	return ""
}

// Mount registers the endpoint routes, behind Options.Guard:
//
//	GET    /api/v1/hooks                          the owner's endpoints
//	POST   /api/v1/hooks                          create one; the reply carries its secret, once
//	GET    /api/v1/hooks/{id}                     one endpoint
//	PATCH  /api/v1/hooks/{id}                     change it (enable: true turns it back on)
//	DELETE /api/v1/hooks/{id}                     delete it and its deliveries
//	POST   /api/v1/hooks/{id}/secret              reveal its secret
//	POST   /api/v1/hooks/{id}/rotate              give it a new secret
//	POST   /api/v1/hooks/{id}/ping                send it a hooks.ping event
//	GET    /api/v1/hooks/{id}/deliveries          its deliveries, newest first
//	POST   /api/v1/hooks/deliveries/{id}/replay   send a delivery again
func Mount(r *router.Router, opt Options) {
	guard := opt.Guard
	if guard == nil {
		guard = auth.Require()
	}
	rt := routes{opt: opt}
	router.Route(r, "GET /api/v1/hooks", rt.hooksList, guard)
	router.Route(r, "POST /api/v1/hooks", rt.hookCreate, guard)
	router.Route(r, "GET /api/v1/hooks/{id}", rt.hookGet, guard)
	router.Route(r, "PATCH /api/v1/hooks/{id}", rt.hookUpdate, guard)
	router.Route(r, "DELETE /api/v1/hooks/{id}", rt.hookDelete, guard)
	router.Route(r, "POST /api/v1/hooks/{id}/secret", rt.hookSecret, guard)
	router.Route(r, "POST /api/v1/hooks/{id}/rotate", rt.hookRotate, guard)
	router.Route(r, "POST /api/v1/hooks/{id}/ping", rt.hookPing, guard)
	router.Route(r, "GET /api/v1/hooks/{id}/deliveries", rt.hookDeliveries, guard)
	router.Route(r, "POST /api/v1/hooks/deliveries/{id}/replay", rt.hookReplay, guard)
}

func (rt routes) hooksList(ctx context.Context, _ *router.Request[router.None]) ([]Endpoint, error) {
	return From(ctx).List(ctx, rt.owner(ctx))
}

func (rt routes) hookCreate(ctx context.Context, req *router.Request[EndpointInput]) (Created, error) {
	e, secret, err := From(ctx).Create(ctx, rt.owner(ctx), req.Body)
	if err != nil {
		return Created{}, err
	}
	req.Status(http.StatusCreated)
	return Created{Endpoint: e, Secret: secret}, nil
}

func (rt routes) hookGet(ctx context.Context, req *router.Request[router.None]) (Endpoint, error) {
	return From(ctx).Get(ctx, rt.owner(ctx), req.Param("id"))
}

func (rt routes) hookUpdate(ctx context.Context, req *router.Request[UpdateInput]) (Endpoint, error) {
	return From(ctx).Update(ctx, rt.owner(ctx), req.Param("id"), req.Body.EndpointInput, req.Body.Enable)
}

func (rt routes) hookDelete(ctx context.Context, req *router.Request[router.None]) (router.None, error) {
	return router.None{}, From(ctx).Delete(ctx, rt.owner(ctx), req.Param("id"))
}

func (rt routes) hookSecret(ctx context.Context, req *router.Request[router.None]) (SecretReply, error) {
	s, err := From(ctx).Secret(ctx, rt.owner(ctx), req.Param("id"))
	return SecretReply{Secret: s}, err
}

func (rt routes) hookRotate(ctx context.Context, req *router.Request[router.None]) (SecretReply, error) {
	s, err := From(ctx).Rotate(ctx, rt.owner(ctx), req.Param("id"))
	return SecretReply{Secret: s}, err
}

func (rt routes) hookPing(ctx context.Context, req *router.Request[router.None]) (Delivery, error) {
	return From(ctx).Ping(ctx, rt.owner(ctx), req.Param("id"))
}

func (rt routes) hookDeliveries(ctx context.Context, req *router.Request[router.None]) ([]Delivery, error) {
	id := req.Param("id")
	if _, err := From(ctx).Get(ctx, rt.owner(ctx), id); err != nil {
		return nil, err
	}
	return From(ctx).Deliveries(ctx, rt.owner(ctx), id, 50)
}

func (rt routes) hookReplay(ctx context.Context, req *router.Request[router.None]) (Delivery, error) {
	return From(ctx).Replay(ctx, rt.owner(ctx), req.Param("id"))
}
