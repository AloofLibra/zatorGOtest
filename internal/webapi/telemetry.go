package webapi

import (
	"context"
	"strings"

	"github.com/AloofLibra/zator/internal/telemetry"
)

func (s *Server) notifyTelemetry() {
	c := telemetry.New(s.paths)
	if strings.TrimSpace(c.Token) == "" || strings.TrimSpace(c.Endpoint) == "" {
		return
	}
	settings, err := c.LoadSettings()
	if err != nil || !settings.Enabled {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), telemetry.SendTimeout)
		defer cancel()
		_ = c.SendStats(ctx)
	}()
}
