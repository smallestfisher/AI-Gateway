package admin

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Mount registers the /api/admin routes on the app. If token is empty admin is
// considered disabled and nothing is mounted.
func Mount(app *fiber.App, st *Store, token string) {
	if token == "" {
		return
	}
	g := app.Group("/api/admin", authMiddleware(token))

	g.Get("/config/version", func(c *fiber.Ctx) error {
		v, err := st.ConfigVersion(c.UserContext())
		if err != nil {
			return writeErr(c, err)
		}
		return c.JSON(fiber.Map{"version": v})
	})
	// Force a config reload (re-publish invalidate) without changing config.
	g.Post("/reload", func(c *fiber.Ctx) error {
		if err := st.invalidate(c.UserContext()); err != nil {
			return writeErr(c, err)
		}
		return c.JSON(fiber.Map{"status": "reloaded"})
	})

	// dashboard
	g.Get("/dashboard/stats", func(c *fiber.Ctx) error {
		stats, err := st.GetDashboardStats(c.UserContext())
		if err != nil {
			return writeErr(c, err)
		}
		return c.JSON(stats)
	})
	g.Get("/dashboard/latency", func(c *fiber.Ctx) error {
		hours := 24 // default 24 hours
		points, err := st.GetLatencyTimeseries(c.UserContext(), hours)
		if err != nil {
			return writeErr(c, err)
		}
		return c.JSON(fiber.Map{"data": points})
	})

	// providers
	g.Get("/providers", func(c *fiber.Ctx) error {
		items, err := st.ListProviders(c.UserContext())
		return listResp(c, items, err)
	})
	g.Post("/providers", func(c *fiber.Ctx) error {
		var p Provider
		if err := c.BodyParser(&p); err != nil {
			return c.Status(400).JSON(errMap("bad_request", err.Error()))
		}
		id, err := st.CreateProvider(c.UserContext(), p)
		return createResp(c, id, err)
	})
	g.Put("/providers/:id", func(c *fiber.Ctx) error {
		var p Provider
		if err := c.BodyParser(&p); err != nil {
			return c.Status(400).JSON(errMap("bad_request", err.Error()))
		}
		return writeErr(c, st.UpdateProvider(c.UserContext(), c.Params("id"), p))
	})
	g.Delete("/providers/:id", func(c *fiber.Ctx) error {
		return writeErr(c, st.DeleteProvider(c.UserContext(), c.Params("id")))
	})

	// models
	g.Get("/models", func(c *fiber.Ctx) error {
		items, err := st.ListModels(c.UserContext())
		return listResp(c, items, err)
	})
	g.Post("/models", func(c *fiber.Ctx) error {
		var m Model
		if err := c.BodyParser(&m); err != nil {
			return c.Status(400).JSON(errMap("bad_request", err.Error()))
		}
		id, err := st.CreateModel(c.UserContext(), m)
		return createResp(c, id, err)
	})
	g.Delete("/models/:id", func(c *fiber.Ctx) error {
		return writeErr(c, st.DeleteModel(c.UserContext(), c.Params("id")))
	})

	// model channels
	g.Get("/model-channels", func(c *fiber.Ctx) error {
		items, err := st.ListChannels(c.UserContext())
		return listResp(c, items, err)
	})
	g.Post("/model-channels", func(c *fiber.Ctx) error {
		var ch ModelChannel
		if err := c.BodyParser(&ch); err != nil {
			return c.Status(400).JSON(errMap("bad_request", err.Error()))
		}
		id, err := st.CreateChannel(c.UserContext(), ch)
		return createResp(c, id, err)
	})
	g.Delete("/model-channels/:id", func(c *fiber.Ctx) error {
		return writeErr(c, st.DeleteChannel(c.UserContext(), c.Params("id")))
	})

	// client profiles
	g.Get("/client-profiles", func(c *fiber.Ctx) error {
		items, err := st.ListProfiles(c.UserContext())
		return listResp(c, items, err)
	})
	g.Post("/client-profiles", func(c *fiber.Ctx) error {
		var p ClientProfile
		if err := c.BodyParser(&p); err != nil {
			return c.Status(400).JSON(errMap("bad_request", err.Error()))
		}
		id, err := st.CreateProfile(c.UserContext(), p)
		return createResp(c, id, err)
	})
	g.Delete("/client-profiles/:id", func(c *fiber.Ctx) error {
		return writeErr(c, st.DeleteProfile(c.UserContext(), c.Params("id")))
	})

	// router policies
	g.Get("/router-policies", func(c *fiber.Ctx) error {
		items, err := st.ListPolicies(c.UserContext())
		return listResp(c, items, err)
	})
	g.Post("/router-policies", func(c *fiber.Ctx) error {
		var p RouterPolicy
		if err := c.BodyParser(&p); err != nil {
			return c.Status(400).JSON(errMap("bad_request", err.Error()))
		}
		id, err := st.UpsertPolicy(c.UserContext(), p)
		return createResp(c, id, err)
	})

	// users + api keys + quotas
	g.Get("/users", func(c *fiber.Ctx) error {
		items, err := st.ListUsers(c.UserContext())
		return listResp(c, items, err)
	})
	g.Post("/users", func(c *fiber.Ctx) error {
		var in struct {
			Name    string `json:"name"`
			Email   string `json:"email"`
			Balance int64  `json:"balance"`
		}
		if err := c.BodyParser(&in); err != nil {
			return c.Status(400).JSON(errMap("bad_request", err.Error()))
		}
		id, err := st.CreateUser(c.UserContext(), in.Name, in.Email, in.Balance)
		return createResp(c, id, err)
	})
	g.Put("/users/:id", func(c *fiber.Ctx) error {
		var in struct {
			Name   string `json:"name"`
			Email  string `json:"email"`
			Status string `json:"status"`
		}
		if err := c.BodyParser(&in); err != nil {
			return c.Status(400).JSON(errMap("bad_request", err.Error()))
		}
		return writeErr(c, st.UpdateUser(c.UserContext(), c.Params("id"), in.Name, in.Email, in.Status))
	})
	g.Get("/users/:id/api-keys", func(c *fiber.Ctx) error {
		items, err := st.ListAPIKeys(c.UserContext(), c.Params("id"))
		return listResp(c, items, err)
	})
	g.Post("/users/:id/api-keys", func(c *fiber.Ctx) error {
		var in struct{ Name string `json:"name"` }
		_ = c.BodyParser(&in)
		raw, id, err := st.IssueAPIKey(c.UserContext(), c.Params("id"), in.Name)
		if err != nil {
			return writeErr(c, err)
		}
		return c.Status(201).JSON(fiber.Map{"id": id, "key": raw}) // plaintext shown once
	})
	g.Delete("/users/:id/api-keys/:key_id", func(c *fiber.Ctx) error {
		return writeErr(c, st.RevokeAPIKey(c.UserContext(), c.Params("key_id")))
	})
	g.Put("/users/:id/quota", func(c *fiber.Ctx) error {
		var in struct {
			Balance    int64    `json:"balance"`
			RPM        int      `json:"rpm"`
			TPM        int      `json:"tpm"`
			Whitelist  []string `json:"whitelist"`
		}
		if err := c.BodyParser(&in); err != nil {
			return c.Status(400).JSON(errMap("bad_request", err.Error()))
		}
		return writeErr(c, st.SetQuota(c.UserContext(), c.Params("id"), in.Balance, in.RPM, in.TPM, in.Whitelist))
	})
}

// authMiddleware rejects requests without a matching Bearer token.
func authMiddleware(token string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		h := c.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") || strings.TrimPrefix(h, "Bearer ") != token {
			return c.Status(401).JSON(errMap("unauthorized", "invalid or missing admin token"))
		}
		return c.Next()
	}
}

// --- response helpers ---

func listResp(c *fiber.Ctx, items any, err error) error {
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(fiber.Map{"data": items})
}

func createResp(c *fiber.Ctx, id string, err error) error {
	if err != nil {
		return writeErr(c, err)
	}
	return c.Status(201).JSON(fiber.Map{"id": id})
}

func writeErr(c *fiber.Ctx, err error) error {
	if err == nil {
		return c.SendStatus(204)
	}
	code, status := classify(err)
	return c.Status(status).JSON(errMap(code, err.Error()))
}

func classify(err error) (string, int) {
	switch {
	case errors.Is(err, ErrValidation):
		return "validation_error", 400
	case errors.Is(err, ErrNotFound):
		return "not_found", 404
	default:
		return "internal_error", 500
	}
}

func errMap(code, message string) fiber.Map {
	return fiber.Map{"error": fiber.Map{"code": code, "message": message}}
}
