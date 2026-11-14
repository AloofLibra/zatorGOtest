package webapi

import (
	"net/http"
	"strings"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/netiface"
)

// ifacesPayload — GET settings?setting=ifaces.
func (s *Server) ifacesPayload(cfg *config.Config) map[string]any {
	d := netiface.Detector{}
	list, _ := d.List()
	sugWAN, sugLAN, src, _ := d.DetectWANLAN()
	wan, _ := cfg.Get(config.IfaceWAN)
	lan, _ := cfg.Get(config.IfaceLAN)
	wan6, _ := cfg.Get(config.IfaceWAN6)
	avail := make([]map[string]any, 0, len(list))
	for _, i := range list {
		avail = append(avail, map[string]any{
			"name": i.Name, "up": i.Up, "addrs": i.Addrs, "kind": i.Kind,
		})
	}
	return map[string]any{
		"wan":       wan,
		"lan":       lan,
		"wan6":      wan6,
		"suggested": map[string]any{"wan": sugWAN, "lan": sugLAN, "source": src},
		"available": avail,
		"needs_autodetect": cfg.NeedsIfaceAutodetect(),
	}
}

func (s *Server) postIfaces(r *http.Request, p params) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(p.Get("action")))
	if action == "" {
		action = "set"
	}
	d := netiface.Detector{}
	var lan, wan string
	switch action {
	case "redetect", "auto":
		var err error
		wan, lan, _, err = d.DetectWANLAN()
		if err != nil {
			return nil, errf(http.StatusInternalServerError, "Не удалось определить интерфейсы: "+err.Error())
		}
		if wan == "" || lan == "" {
			return nil, errf(http.StatusConflict, "Автоопределение неполное (WAN="+wan+" LAN="+lan+"). Задайте вручную.")
		}
	case "set":
		lan = strings.TrimSpace(p.Get("lan"))
		wan = strings.TrimSpace(p.Get("wan"))
		if lan == "" {
			lan = strings.TrimSpace(p.Get("iface_lan"))
		}
		if wan == "" {
			wan = strings.TrimSpace(p.Get("iface_wan"))
		}
		if lan == "" || wan == "" {
			return nil, errf(http.StatusBadRequest, "Укажите lan и wan")
		}
		if !netiface.ValidList(lan) || !netiface.ValidList(wan) {
			return nil, errf(http.StatusBadRequest, "Некорректное имя интерфейса")
		}
	default:
		return nil, errf(http.StatusBadRequest, "Некорректное действие: "+action)
	}
	if err := s.editConfigs(func(c *config.Config) error {
		return c.SetIfaces(lan, wan)
	}); err != nil {
		return nil, err
	}
	out := s.ifacesPayload(mustConfig(s))
	out["ok"] = true
	out["restarted"] = s.maybeRestart(r.Context(), p)
	out["restart_required"] = true
	return out, nil
}

func mustConfig(s *Server) *config.Config {
	c, _ := s.readConfig()
	return c
}
