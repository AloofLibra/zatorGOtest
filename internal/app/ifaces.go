package app

import (
	"fmt"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/netiface"
	"github.com/AloofLibra/zator/internal/platform"
)

// ensureNetworkIfaces проставляет IFACE_LAN/WAN по default-route и bridge,
// если в конфиге они пусты или остались шаблонными eth0/eth1.
// На OpenWrt переменные IFACE_* не используются firewall-ом — пропуск.
func (a *App) ensureNetworkIfaces(cfg *config.Config) (changed bool, err error) {
	if cfg == nil || !cfg.NeedsIfaceAutodetect() {
		return false, nil
	}
	if a.Platform.Kind == platform.KindOpenWRT {
		return false, nil
	}
	wan, lan, _, err := (netiface.Detector{}).DetectWANLAN()
	if err != nil {
		return false, err
	}
	if wan == "" || lan == "" {
		return false, fmt.Errorf("автоопределение интерфейсов неполное: WAN=%q LAN=%q", wan, lan)
	}
	if err := cfg.SetIfaces(lan, wan); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) isRouterHost() bool {
	return a.Platform.Kind == platform.KindOpenWRT || a.Platform.Kind == platform.KindEntware
}
