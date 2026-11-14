package app

import (
	"fmt"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/netiface"
)

// ensureNetworkIfaces проставляет IFACE_LAN/WAN по default-route и bridge,
// если в конфиге они пусты или остались шаблонными eth0/eth1.
func (a *App) ensureNetworkIfaces(cfg *config.Config) (changed bool, err error) {
	if cfg == nil || !cfg.NeedsIfaceAutodetect() {
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
