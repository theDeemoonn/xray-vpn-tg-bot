package handler

import (
	"log/slog"
	"net/http"

	"xray-vpn-tg-bot/internal/service"

	"github.com/go-chi/chi/v5"
)

// --- Health Handler ---

// HealthCheck returns a simple OK status.
func HealthCheck(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("Health check requested")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}
}

// --- Users Handler ---

type UsersHandler struct {
	userService service.UserService
	logger      *slog.Logger
}

func NewUsersHandler(userService service.UserService, logger *slog.Logger) *UsersHandler {
	return &UsersHandler{
		userService: userService,
		logger:      logger,
	}
}

// Routes sets up the routes for the users handler.
func (h *UsersHandler) Routes() chi.Router {
	r := chi.NewRouter()
	// Define user-related routes here
	// Example:
	// r.Get("/", h.listUsers)
	// r.Get("/{userID}", h.getUser)

	// Placeholder route
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Users endpoint placeholder"))
	})
	return r
}

// --- Servers Handler ---

type ServersHandler struct {
	serverService service.ServerService
	logger        *slog.Logger
}

func NewServersHandler(serverService service.ServerService, logger *slog.Logger) *ServersHandler {
	return &ServersHandler{
		serverService: serverService,
		logger:        logger,
	}
}

// Routes sets up the routes for the servers handler.
func (h *ServersHandler) Routes() chi.Router {
	r := chi.NewRouter()
	// Add specific routes here using r.Method(...) referencing methods in h
	return r
}

// --- Subscriptions Handler ---

type SubscriptionsHandler struct {
	subscriptionService service.SubscriptionService
	logger              *slog.Logger
}

func NewSubscriptionsHandler(subService service.SubscriptionService, logger *slog.Logger) *SubscriptionsHandler {
	return &SubscriptionsHandler{
		subscriptionService: subService,
		logger:              logger,
	}
}

// Routes sets up the routes for the subscriptions handler.
func (h *SubscriptionsHandler) Routes() chi.Router {
	r := chi.NewRouter()
	// Add specific routes here
	return r
}

// Add other handlers (PlansHandler, etc.) here
