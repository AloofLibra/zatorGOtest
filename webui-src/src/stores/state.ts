import { fetchState } from '../api/endpoints'
import { backups } from './backups'
import {
  fallbackSettings, ifacesSettings, modeSettings, ports, provider, settingsLoaded, telemetrySettings, wssizeRampSettings,
  tlsBlobSettings, udpGamesSettings, wgBlobSettings, wgStateSettings,
} from './settings'
import { authRequired, locks, scope, scopes, status, statusLoaded, versionInfo, versionInfoCheckedAt, versionInfoFresh } from './status'

// Одна агрегирующая загрузка вместо ~15 отдельных CGI: state.cgi читает
// всё состояние за один процесс на роутере.
export async function fetchAndApplyState() {
  const payload = await fetchState(scope.value)
  authRequired.value = payload.auth_required !== false
  status.value = payload.status
  versionInfo.value = payload.version
  versionInfoCheckedAt.value = Date.now()
  versionInfoFresh.value = true
  locks.value = payload.status.profiles || []
  scopes.value = payload.scopes
  tlsBlobSettings.value = payload.tls_blob
  wgBlobSettings.value = payload.wg_blob
  wgStateSettings.value = payload.wg_state
  fallbackSettings.value = payload.fallback
  udpGamesSettings.value = payload.udp_games
  wssizeRampSettings.value = payload.wssize_ramp
  ifacesSettings.value = payload.ifaces || null
  modeSettings.auto_mode = payload.auto_mode
  modeSettings.hostlist = payload.hostlist
  modeSettings.rst_guard = payload.rst_guard
  modeSettings.reasm = payload.reasm
  modeSettings.dns_desync = payload.dns_desync
  ports.value = payload.ports
  provider.value = payload.provider
  telemetrySettings.value = payload.telemetry
  backups.value = payload.backups
  statusLoaded.value = true
  settingsLoaded.value = true
}
