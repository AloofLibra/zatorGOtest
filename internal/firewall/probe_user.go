package firewall

import (
	"os/user"
	"strconv"
)

// PreferredProbeUserNames — кандидаты для изолированного nfqws2.
// Числовой UID без записи в passwd nfqws2 отвергает ("non-existent username").
var PreferredProbeUserNames = []string{"zator-ss", "nobody", "nfsnobody"}

// ResolveProbeUser выбирает имя и UID для пробника SuperSweep.
// Сначала ищет PreferredProbeUserNames в passwd. Пустое имя — пользователя нет:
// nfqws2 отвергает числовой --user= без записи в passwd.
func ResolveProbeUser() (name string, uid, gid uint32) {
	for _, n := range PreferredProbeUserNames {
		u, err := user.Lookup(n)
		if err != nil {
			continue
		}
		id, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			continue
		}
		gid64, err := strconv.ParseUint(u.Gid, 10, 32)
		if err != nil {
			gid64 = id
		}
		return n, uint32(id), uint32(gid64)
	}
	return "", 0, 0
}

// ResolveTrafficUID returns the UID of the current Go process, which opens
// the HTTP sockets inspected by SuperSweep. It is intentionally distinct
// from ResolveProbeUser: only nfqws2 itself runs as the isolated probe user.
func ResolveTrafficUID() uint32 {
	u, err := user.Current()
	if err != nil {
		return 0
	}
	id, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(id)
}
