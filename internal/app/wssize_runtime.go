package app

import (
	"context"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/firewall"
)

// ApplyWssizeFirewall reconciles only Zator-owned WSSIZE rules with zapret2 config.
func (a *App) ApplyWssizeFirewall(ctx context.Context) error {
	if a == nil || a.Platform.Kind == "" {
		return nil
	}
	cfg, err := config.Load(a.Paths.ZapretConfig)
	if err != nil {
		return err
	}
	wcfg, err := firewall.WssizeConfigFromGetter(cfg.Get, a.Paths.ZatorRoot)
	if err != nil {
		return err
	}
	_, err = firewall.NewWssize(wcfg, firewall.ExecRunner{}).Apply(ctx)
	return err
}

// RemoveWssizeFirewall removes Zator-owned WSSIZE rules during service stop.
func (a *App) RemoveWssizeFirewall(ctx context.Context) error {
	if a == nil || a.Platform.Kind == "" {
		return nil
	}
	cfg, err := config.Load(a.Paths.ZapretConfig)
	if err != nil {
		return err
	}
	wcfg, err := firewall.WssizeConfigFromGetter(cfg.Get, a.Paths.ZatorRoot)
	if err != nil {
		return err
	}
	return firewall.NewWssize(wcfg, firewall.ExecRunner{}).Remove(ctx)
}
