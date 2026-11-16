<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, formBody } from '../api/client'
import {
  applySetting, backups as backupsApi, fetchClientScopes, fetchRecommendations, fetchSetting, updateClientScope,
} from '../api/endpoints'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import { confirmDialog } from '../stores/confirm'
import {
  fallbackSettings, ifacesSettings, modeSettings, ports, provider, settingsLoaded, telemetrySettings,
  tlsBlobSettings, udpGamesSettings, wgBlobSettings, wgStateSettings, wssizeRampSettings,
  refreshFallbackSettings, refreshIfacesSettings, refreshModeSetting, refreshPorts, refreshProvider,
  refreshTlsBlobSettings, refreshUdpGamesSettings, refreshWgBlobSettings, refreshWgStateSettings,
  refreshWssizeRampSettings,
} from '../stores/settings'
import type { ClientScopeMapping, Recommendations, WssizeRampSettings } from '../api/types'
import { backups } from '../stores/backups'
import { fetchAndApplyState } from '../stores/state'
import { refreshAll, scopes, status } from '../stores/status'
import { announceRestart, restartSuffix, showToast } from '../stores/toast'

const route = useRoute()
const router = useRouter()

const sections = [
  { id: 'sec-updates', label: 'Обновления', group: 'Система' },
  { id: 'sec-provider', label: 'Провайдер', group: 'Система' },
  { id: 'sec-telemetry', label: 'Телеметрия', group: 'Система' },
  { id: 'sec-backups', label: 'Бэкапы', group: 'Система' },
  { id: 'sec-modes', label: 'Режимы', group: 'Обход' },
  { id: 'sec-tls', label: 'TLS', group: 'Обход' },
  { id: 'sec-wg', label: 'WireGuard', group: 'Обход' },
  { id: 'sec-wssize', label: 'WSSIZE', group: 'Обход' },
  { id: 'sec-firewall', label: 'Firewall', group: 'Сеть' },
  { id: 'sec-ifaces', label: 'Интерфейсы', group: 'Сеть' },
  { id: 'sec-ports', label: 'Порты', group: 'Сеть' },
  { id: 'sec-scopes', label: 'Scopes', group: 'Сеть' },
  { id: 'sec-config', label: 'Конфиг', group: 'Сервис' },
  { id: 'sec-errors', label: 'Ошибки', group: 'Сервис' },
]

const settingGroups = [
  { id: 'grp-system', title: 'Система', desc: 'Обновления, провайдер, телеметрия и резервные копии.' },
  { id: 'grp-bypass', title: 'Обход DPI', desc: 'Режимы стратегий, TLS/WG blob и wssize.' },
  { id: 'grp-net', title: 'Сеть', desc: 'Firewall, интерфейсы WAN/LAN, порты NFQUEUE и client scopes.' },
  { id: 'grp-svc', title: 'Сервис', desc: 'Сырой конфиг zapret2 и журнал ошибок.' },
]

const modes = [
  { key: 'auto_mode', post: 'auto_mode_state', title: 'Авторотация TCP/HTTP', field: 'enabled' as const },
  { key: 'hostlist', post: 'hostlist_state', title: 'Autohostlist', field: 'auto' as const },
  { key: 'fallback', post: 'fallback_state', title: 'Безразборный режим', field: 'enabled' as const, fromFallback: true },
  { key: 'udp-games', post: 'udp_games_state', title: 'UDP Games 1026-65531', field: 'enabled' as const, fromUdp: true },
  { key: 'dns_desync', post: 'dns_desync_state', title: 'Антиспуф DNS (весь UDP/53)', field: 'enabled' as const },
  { key: 'rst_guard', post: 'rst_guard_state', title: 'RST guard', field: 'enabled' as const },
  { key: 'reasm', post: 'reasm_state', title: 'reasm-disable', field: 'enabled' as const },
]

const fw = ref(status.value?.fwtype || 'iptables')
const offload = ref(status.value?.flowoffload || 'donttouch')
watch(status, (s) => {
  if (s?.fwtype) fw.value = s.fwtype
  if (s?.flowoffload) offload.value = s.flowoffload
}, { immediate: true })

const ifaceWan = ref('')
const ifaceLan = ref('')
watch(ifacesSettings, (i) => {
  if (!i) return
  ifaceWan.value = i.wan || i.suggested?.wan || ''
  ifaceLan.value = i.lan || i.suggested?.lan || ''
}, { immediate: true })

const ifaceOptions = computed(() => {
  const list = ifacesSettings.value?.available || []
  const names = list.map((x) => x.name)
  for (const extra of [ifaceWan.value, ifaceLan.value, ifacesSettings.value?.suggested?.wan, ifacesSettings.value?.suggested?.lan]) {
    if (extra && !names.includes(extra)) names.push(extra)
  }
  return names
})

const tlsMode = ref('fake_default_tls')
const tlsBlob = ref('')
watch(tlsBlobSettings, (t) => {
  if (!t) return
  // API отдаёт fake_default_tls | maxru | mixed — в UI: встроенный vs файл.
  tlsMode.value = t.current_mode === 'fake_default_tls' ? 'fake_default_tls' : 'custom'
  tlsBlob.value = t.current_blob || ''
}, { immediate: true })

const wgOn = ref(false)
const wgBlob = ref('')
const wgRepeats = ref(2)
watch(wgStateSettings, (w) => { if (w) wgOn.value = !!w.enabled }, { immediate: true })
watch(wgBlobSettings, (w) => {
  if (!w) return
  wgBlob.value = w.current_blob || ''
  wgRepeats.value = Number(w.current_repeats || 2)
}, { immediate: true })

const portProto = ref<'tcp' | 'udp'>('tcp')
const portValue = ref('')
const providerName = ref('')
const providerCity = ref('')
const recommendations = ref<Recommendations | null>(null)
const recommendationsBusy = ref(false)
const providerKnown = computed(() => {
  const name = String(provider.value?.provider || '').trim()
  return !!name && name !== 'Не определён' && name !== 'Unknown'
})
const providerDraftSaved = computed(() => {
  const parts = String(provider.value?.provider || '').split(' - ')
  return providerName.value.trim() === (parts[0] || '') && providerCity.value.trim() === parts.slice(1).join(' - ')
})
const recommendationProfiles = computed(() => Object.entries(recommendations.value?.profiles || {}))
watch(provider, (p) => {
  const raw = String(p?.provider || '')
  if (raw === 'Не определён') {
    providerName.value = ''
    providerCity.value = ''
    return
  }
  const parts = raw.split(' - ')
  providerName.value = parts[0] || ''
  providerCity.value = parts.slice(1).join(' - ')
}, { immediate: true })

const scopeForm = reactive({ enable: false, mask: '0xff00', shift: '8', max: '255' })
const clientMappings = ref<ClientScopeMapping[]>([])
const clientIP = ref('')
const clientScopeID = ref('1')
watch(scopes, (s) => {
  if (!s) return
  scopeForm.enable = !!s.enabled
  if (s.mask != null && String(s.mask) !== '') scopeForm.mask = String(s.mask)
  if (s.shift != null) scopeForm.shift = String(s.shift)
  if (s.max_scope != null) scopeForm.max = String(s.max_scope)
}, { immediate: true })

const configText = ref('')
const configPath = ref('')
const configLoaded = ref(false)
const errors = ref({ available: false, content: '', path: '', lines: 0 })
const updateInfo = ref<{ update_available?: boolean; release?: string; checked_at?: string; error?: string } | null>(null)

const wssizeOn = ref(false)
const wssizeMode = ref<WssizeRampSettings['mode']>('full')
const wssizeScale = ref(1)
watch(wssizeRampSettings, (w) => {
  if (!w) return
  wssizeOn.value = w.enabled
  wssizeMode.value = w.mode
  wssizeScale.value = w.scale
}, { immediate: true })

const portList = computed(() => {
  const info = ports.value?.[portProto.value]
  return info?.user || []
})

async function refresh() {
  try {
    await withBusy('refresh-settings', fetchAndApplyState)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

function modeOn(m: typeof modes[number]) {
  if (m.fromFallback) return fallbackSettings.value?.enabled
  if (m.fromUdp) return udpGamesSettings.value?.enabled
  const d = modeSettings[m.key]
  return m.field === 'auto' ? d?.auto : d?.enabled
}

async function toggleMode(m: typeof modes[number], on: boolean) {
  try {
    await withBusy(`mode-${m.key}`, async () => {
      announceRestart()
      const res = await applySetting.mode_state(m.post, on)
      showToast(`${m.title}: ${on ? 'вкл' : 'выкл'}.${restartSuffix(res)}`)
      if (m.fromFallback) await refreshFallbackSettings()
      else if (m.fromUdp) await refreshUdpGamesSettings()
      else await refreshModeSetting(m.key)
      await refreshAll()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveSelect(setting: string, value: string, label: string) {
  try {
    await withBusy(`sel-${setting}`, async () => {
      announceRestart()
      const res = await api('/api/settings', formBody({ setting, value }))
      showToast(`${label}: ${value}.${restartSuffix(res)}`)
      await fetchAndApplyState()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveTls() {
  if (tlsMode.value !== 'fake_default_tls' && !tlsBlob.value) {
    showToast('Выберите файл TLS blob', 'error')
    return
  }
  try {
    await withBusy('tls', async () => {
      announceRestart()
      const res = tlsMode.value === 'fake_default_tls'
        ? await applySetting.tls_blob('fake_default_tls')
        : await applySetting.tls_blob(tlsBlob.value)
      showToast(`TLS blob сохранён.${restartSuffix(res)}`)
      await refreshTlsBlobSettings()
      await refreshAll()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveWg(restart = true) {
  try {
    const repeats = Number(wgRepeats.value)
    if (!Number.isInteger(repeats) || repeats < 2 || repeats > 99) {
      showToast('Repeats WireGuard: целое число 2–99', 'error')
      return
    }
    if (wgOn.value && !String(wgBlob.value || '').trim()) {
      showToast('Выберите WG blob перед включением', 'error')
      return
    }
    await withBusy('wg', async () => {
      if (restart) announceRestart()
      // Один рестарт в конце — иначе двойной maybeRestart.
      await applySetting.wg_state(wgOn.value, false)
      if (wgBlob.value) await applySetting.wg_blob(wgBlob.value, false)
      await applySetting.wg_repeats(repeats, restart)
      showToast('WireGuard сохранён')
      await refreshWgBlobSettings()
      await refreshWgStateSettings()
      await refreshAll()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function addPort() {
  try {
    const raw = String(portValue.value ?? '').trim()
    if (!raw) {
      showToast('Укажите порт или список портов', 'error')
      return
    }
    await withBusy('port-add', async () => {
      announceRestart()
      const res = await applySetting.ports_add(portProto.value, raw)
      portValue.value = ''
      showToast(`Порты добавлены.${restartSuffix(res)}`)
      await refreshPorts()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function removePort(v: string) {
  try {
    await withBusy('port-rm', async () => {
      announceRestart()
      await applySetting.ports_remove(portProto.value, v)
      await refreshPorts()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveProvider() {
  try {
    await withBusy('provider', async () => {
      await applySetting.provider_set(providerName.value.trim(), providerCity.value.trim())
      showToast('Провайдер сохранён')
      await refreshProvider()
      await refreshAll()
      recommendations.value = null
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function setTelemetryConsent(enabled: boolean) {
  try {
    await withBusy('telemetry', async () => {
      const result = await applySetting.telemetry_consent(enabled)
      await fetchAndApplyState()
      if (!enabled) showToast('Отправка статистики отключена')
      else if (!result.configured) showToast('Согласие сохранено. Сервер отправки не настроен, данные не отправляются.')
      else if (result.sent) showToast('Согласие сохранено, статистика отправлена')
      else showToast('Согласие сохранено, отправить статистику не удалось', 'error')
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveScope() {
  try {
    await withBusy('scope', async () => {
      announceRestart()
      const res = await api<{ ok?: boolean; enabled?: boolean; warning?: string }>('/api/settings', formBody({
        setting: 'client_scope',
        client_scope_enable: scopeForm.enable ? '1' : '0',
        client_scope_mark_mask: String(scopeForm.mask ?? '').trim(),
        client_scope_mark_shift: String(scopeForm.shift ?? '').trim(),
        client_scope_mark_max: String(scopeForm.max ?? '').trim(),
      }))
      await fetchAndApplyState()
      const enabled = res.enabled ?? !!scopes.value?.enabled
      scopeForm.enable = !!enabled
      if (res.warning) {
        showToast(res.warning, 'warning')
      } else {
        showToast(enabled ? 'Client scopes включены' : 'Client scopes выключены')
      }
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function refreshClientScopes() {
  const data = await fetchClientScopes()
  clientMappings.value = data.mappings || []
}

async function addClientScope() {
  const ip = clientIP.value.trim()
  const id = Number(clientScopeID.value)
  if (!ip || !Number.isInteger(id) || id < 1 || id > Number(scopes.value.max_scope || 255)) {
    showToast(`Укажите IP и номер scope от 1 до ${scopes.value.max_scope || 255}`, 'error')
    return
  }
  try {
    await withBusy('client-scope-add', async () => {
      announceRestart()
      const res = await updateClientScope('add', ip, `mark:${id}`)
      clientIP.value = ''
      showToast(`Клиент ${ip} добавлен в mark:${id}.${restartSuffix(res)}`)
      await refreshClientScopes()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function removeClientScope(mapping: ClientScopeMapping) {
  try {
    await withBusy(`client-scope-rm-${mapping.ip}`, async () => {
      announceRestart()
      const res = await updateClientScope('remove', mapping.ip)
      showToast(`Клиент ${mapping.ip} удалён.${restartSuffix(res)}`)
      await refreshClientScopes()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function loadConfig() {
  try {
    await withBusy('cfg-load', async () => {
      const data = await api<{ content?: string; path?: string; error?: string }>('/api/settings?setting=config_raw')
      if (data.error) throw new Error(data.error)
      configText.value = data.content || ''
      configPath.value = data.path || ''
      configLoaded.value = true
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveConfig() {
  const ok = await confirmDialog({
    title: 'Записать config?',
    message: ['Файл zapret2/config будет перезаписан.', 'zapret2 перезапустится.'],
    danger: true,
  })
  if (!ok) return
  try {
    await withBusy('cfg-save', async () => {
      announceRestart()
      await api('/api/settings', formBody({ setting: 'config_raw', value: configText.value }))
      showToast('Конфиг сохранён')
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function loadErrors() {
  try {
    await withBusy('err-load', async () => {
      const data = await api<typeof errors.value>('/api/settings?setting=nfqws2_errors')
      errors.value = {
        available: !!data.available,
        content: data.content || '',
        path: data.path || '',
        lines: data.lines || 0,
      }
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function checkUpdates() {
  try {
    await withBusy('upd-check', async () => {
      updateInfo.value = await fetchSetting.update_check()
      if (updateInfo.value?.error) showToast(updateInfo.value.error, 'error')
      else showToast(updateInfo.value?.update_available ? 'Есть обновление' : 'Актуально', updateInfo.value?.update_available ? 'info' : 'success')
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function applyUpdates() {
  const ok = await confirmDialog({
    title: 'Установить обновление ZatorGO?',
    message: ['Будут скачаны файлы релиза. Может занять несколько минут.'],
  })
  if (!ok) return
  try {
    await withBusy('upd-apply', async () => {
      const res = await api<{
        up_to_date?: boolean
        installed?: number
        release?: string
        restart_required?: boolean
      }>(
        '/api/settings',
        { ...formBody({ setting: 'update_apply' }), timeoutMs: 200_000 },
      )
      if (res.up_to_date) {
        showToast('Уже актуально')
        await fetchAndApplyState()
      } else if (res.restart_required) {
        // WebUI скоро перезапустится — не гоняем state поверх умирающего процесса.
        showToast(`Обновлено (файлов: ${res.installed ?? 0}). Перезапуск WebUI…`)
      } else {
        showToast(`Обновлено (файлов: ${res.installed ?? 0})`)
        await fetchAndApplyState()
      }
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function downloadBackup(name: string) {
  try {
    await withBusy('bak-dl', async () => {
      await backupsApi.download(name)
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveWssize() {
  if (!wssizeRampSettings.value) return
  if (!Number.isInteger(wssizeScale.value) || wssizeScale.value < 0 || wssizeScale.value > 14) {
    showToast('Scale должен быть 0–14', 'error')
    return
  }
  try {
    await withBusy('wssize', async () => {
      announceRestart()
      const res = await applySetting.wssize_ramp({
        ...wssizeRampSettings.value!,
        enabled: wssizeOn.value,
        mode: wssizeMode.value,
        scale: wssizeScale.value,
      })
      showToast(`wssize_ramp сохранён.${restartSuffix(res)}`)
      await refreshWssizeRampSettings()
      await refreshAll()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function saveIfaces() {
  if (!ifaceWan.value.trim() || !ifaceLan.value.trim()) {
    showToast('Укажите WAN и LAN', 'error')
    return
  }
  try {
    await withBusy('ifaces', async () => {
      announceRestart()
      const res = await applySetting.ifaces({ lan: ifaceLan.value.trim(), wan: ifaceWan.value.trim() })
      showToast(`Интерфейсы: LAN=${ifaceLan.value}, WAN=${ifaceWan.value}.${restartSuffix(res)}`)
      await refreshIfacesSettings()
      await refreshAll()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function redetectIfaces() {
  try {
    await withBusy('ifaces-auto', async () => {
      announceRestart()
      const res = await applySetting.ifaces({ action: 'redetect' })
      showToast(`Автоопределение: LAN=${res.lan || '—'}, WAN=${res.wan || '—'}.${restartSuffix(res)}`)
      await refreshIfacesSettings()
      await refreshAll()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function redetectProvider() {
  try {
    await withBusy('provider-redetect', async () => {
      await applySetting.provider_redetect()
      showToast('Провайдер переопределён')
      await refreshProvider()
      await refreshAll()
      recommendations.value = null
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function loadRecommendations() {
  try {
    recommendationsBusy.value = true
    recommendations.value = await fetchRecommendations()
  } catch (e) {
    showToast((e as Error).message, 'error')
  } finally {
    recommendationsBusy.value = false
  }
}

async function uploadBackup(ev: Event) {
  const input = ev.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  try {
    await withBusy('bak-upload', async () => {
      const res = await backupsApi.upload(file)
      showToast(`Загружен: ${res.name}`)
      await fetchAndApplyState()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function createBackup() {
  try {
    await withBusy('bak-create', async () => {
      const res = await backupsApi.create()
      showToast(`Бэкап: ${res.name}`)
      await fetchAndApplyState()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function deleteBackup(name: string) {
  const ok = await confirmDialog({ title: 'Удалить бэкап?', message: name, danger: true })
  if (!ok) return
  try {
    await withBusy('bak-del', async () => {
      await backupsApi.delete(name)
      await fetchAndApplyState()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function restoreBackup(name: string) {
  const ok = await confirmDialog({
    title: 'Восстановить бэкап?',
    message: [
      `Архив: ${name}`,
      'Будут восстановлены конфиг, списки доменов и локи стратегий.',
      'zapret2 будет перезапущен.',
    ],
    danger: true,
    confirmText: 'Восстановить',
  })
  if (!ok) return
  try {
    await withBusy('bak-restore', async () => {
      announceRestart()
      const res = await backupsApi.restore(name, 'full')
      showToast(`Восстановлено файлов: ${res.files ?? 0}.${restartSuffix(res)}`)
      await fetchAndApplyState()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

const panelAliases: Record<string, string> = {
  updates: 'sec-updates', provider: 'sec-provider', backups: 'sec-backups',
  modes: 'sec-modes', 'auto-mode': 'sec-modes', fallback: 'sec-modes', hostlist: 'sec-modes',
  'udp-games': 'sec-modes', 'dns-desync': 'sec-modes', 'rst-guard': 'sec-modes',
  reasm: 'sec-modes',
  tls: 'sec-tls', 'tls-blob': 'sec-tls',
  wg: 'sec-wg', wireguard: 'sec-wg',
  wssize: 'sec-wssize', 'wssize-ramp': 'sec-wssize',
  firewall: 'sec-firewall', fwtype: 'sec-firewall', flowoffload: 'sec-firewall',
  ifaces: 'sec-ifaces', iface: 'sec-ifaces', interfaces: 'sec-ifaces',
  ports: 'sec-ports', scopes: 'sec-scopes', 'client-scope': 'sec-scopes',
  config: 'sec-config', errors: 'sec-errors',
}

function panelIdFromRoute(): string {
  const raw = String(route.params.panel || (route.hash || '').replace(/^#/, '') || '')
  if (!raw) return ''
  return panelAliases[raw] || (raw.startsWith('sec-') ? raw : `sec-${raw}`)
}

function scrollToPanel(forceId?: string) {
  const id = forceId || panelIdFromRoute()
  if (!id || !settingsLoaded.value) return
  const el = document.getElementById(id)
  if (!el) return
  // Учёт sticky status-strip + settings-nav
  const nav = document.querySelector('.settings-nav') as HTMLElement | null
  const strip = document.querySelector('.status-strip') as HTMLElement | null
  const offset = (nav?.offsetHeight || 0) + (strip?.offsetHeight || 0) + 12
  const top = el.getBoundingClientRect().top + window.scrollY - offset
  window.scrollTo({ top: Math.max(0, top), behavior: 'smooth' })
}

function goSection(id: string, ev?: Event) {
  ev?.preventDefault()
  const slug = id.replace(/^sec-/, '')
  const target = `/settings/${slug}`
  if (route.path === target || route.fullPath.endsWith(`/settings/${slug}`)) {
    void nextTick(() => scrollToPanel(id))
    return
  }
  void router.push(target).then(() => {
    void nextTick(() => {
      requestAnimationFrame(() => scrollToPanel(id))
    })
  })
}

watch(() => [route.params.panel, route.hash, settingsLoaded.value] as const, async () => {
  if (!settingsLoaded.value) return
  await nextTick()
  requestAnimationFrame(() => scrollToPanel())
}, { immediate: true })

onMounted(async () => {
  // Последовательно: параллельный withBusy раньше сбрасывал busy чужим finally.
  await loadConfig()
  await loadErrors()
  try { await refreshClientScopes() } catch (e) { showToast((e as Error).message, 'error') }
  await nextTick()
  scrollToPanel()
})
</script>

<template>
  <section>
    <div class="page-head">
      <div>
        <h1>Настройки</h1>
        <p>Система, обход DPI, сеть и сервис — якоря сверху.</p>
      </div>
      <div class="actions">
        <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'refresh-settings' }" @click="refresh">Обновить</button>
      </div>
    </div>

    <nav class="settings-nav" aria-label="Разделы настроек">
      <a
        v-for="s in sections"
        :key="s.id"
        href="#"
        :class="{ 'is-active': panelIdFromRoute() === s.id }"
        @click="goSection(s.id, $event)"
      >{{ s.label }}</a>
    </nav>

    <div v-if="!settingsLoaded" class="loading">Загрузка…</div>
    <template v-else>
      <div id="grp-system" class="settings-group">
        <h2>{{ settingGroups[0].title }}</h2>
        <p>{{ settingGroups[0].desc }}</p>
      </div>

      <section id="sec-updates" class="panel">
        <div class="panel-h"><h2>Обновления</h2></div>
        <p class="panel-d">Проверка и установка релизов ZatorGO.</p>
        <p v-if="updateInfo" class="panel-d">
          {{ updateInfo.update_available ? 'Есть обновление' : 'Актуально' }}
          <template v-if="updateInfo.release"> · {{ updateInfo.release }}</template>
          <template v-if="updateInfo.checked_at"> · {{ updateInfo.checked_at }}</template>
        </p>
        <div class="actions">
          <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'upd-check' }" @click="checkUpdates">Проверить</button>
          <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'upd-apply' }" @click="applyUpdates">Установить</button>
        </div>
      </section>

      <section id="sec-provider" class="panel">
        <div class="panel-h"><h2>Провайдер</h2></div>
        <p class="panel-d">Нужен для рекомендаций стратегий. Можно задать вручную или определить автоматически.</p>
        <div class="grid-2">
          <label class="field"><span>Имя</span><input v-model="providerName" :disabled="busyActive"></label>
          <label class="field"><span>Город</span><input v-model="providerCity" :disabled="busyActive"></label>
        </div>
        <div class="actions" style="margin-top:0.8rem">
          <button type="button" class="primary" :disabled="busyActive || !providerName.trim()" @click="saveProvider">Сохранить</button>
          <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'provider-redetect' }" @click="redetectProvider">Определить заново</button>
        </div>
        <div style="border-top:1px solid var(--line);padding-top:0.9rem;margin-top:0.5rem">
          <h3>Рекомендации стратегий</h3>
          <p class="panel-d">В запросе передаётся название провайдера; сервер также видит IP роутера. Подсказки основаны на статистике успешных проверок и кешируются на сутки.</p>
          <button type="button" :disabled="!providerKnown || !providerDraftSaved || recommendationsBusy || busyActive" @click="loadRecommendations">
            {{ recommendationsBusy ? 'Загрузка…' : 'Загрузить рекомендации' }}
          </button>
          <p v-if="!providerKnown" class="panel-d">Сначала определите или сохраните провайдера.</p>
          <p v-else-if="!providerDraftSaved" class="panel-d">Сначала сохраните изменённое название провайдера.</p>
          <template v-if="recommendations">
            <p class="panel-d">
              <template v-if="recommendations.status === 'ready'">Статистика собрана с {{ recommendations.samples }} установок.</template>
              <template v-else-if="recommendations.status === 'insufficient'">Пока мало данных: {{ recommendations.samples }} установок, требуется {{ recommendations.minimum }}.</template>
              <template v-else-if="recommendations.status === 'stale'">Сервер временно недоступен; показан сохранённый результат.</template>
              <template v-else-if="recommendations.status === 'unknown_provider'">Провайдер не определён.</template>
              <template v-else>Рекомендации пока недоступны.</template>
            </p>
            <div v-for="[profileId, item] in recommendationProfiles" :key="profileId" class="domain-item">
              <div>
                <strong>Профиль {{ profileId }}</strong>
                <span v-if="item.clone_recommended"> · клоны рекомендуются ({{ item.clone_pct }}% против {{ item.classic_pct }}%)</span>
                <div v-if="item.top.length" class="panel-d">
                  Лучшие стратегии: {{ item.top.map((x) => `${x.strategy} (${x.success_pct}%, ${x.mode})`).join(', ') }}
                </div>
                <div v-else class="panel-d">Нет достаточной статистики по стратегиям.</div>
              </div>
            </div>
          </template>
        </div>
      </section>

      <section id="sec-telemetry" class="panel">
        <div class="panel-h"><h2>Анонимная статистика</h2></div>
        <p class="panel-d">При согласии отправляются псевдоним установки, провайдер, номера стратегий, выбранные TLS blob и режим fake, тип устройства и наличие WebUI. Домены и содержимое трафика не отправляются.</p>
        <p class="panel-d">Отправка выключена, пока вы явно не дадите согласие.</p>
        <label class="toggle">
          <input type="checkbox" :checked="!!telemetrySettings?.enabled" :disabled="busyActive"
            @change="setTelemetryConsent(($event.target as HTMLInputElement).checked)">
          <span>Разрешить отправку статистики</span>
          <span class="chip" :class="{ on: telemetrySettings?.enabled }">{{ telemetrySettings?.enabled ? 'вкл' : 'выкл' }}</span>
        </label>
        <p class="panel-d" v-if="telemetrySettings?.enabled && !telemetrySettings.configured">
          Согласие сохранено, но endpoint и token отправки не настроены; статистика не отправляется.
        </p>
      </section>

      <section id="sec-backups" class="panel">
        <div class="panel-h"><h2>Бэкапы</h2></div>
        <p class="panel-d">Снимок конфига и локов. Можно скачать или восстановить из файла.</p>
        <div class="actions">
          <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'bak-create' }" @click="createBackup">Создать</button>
          <label class="ghost" style="padding:0.65rem 1rem;border:1px solid var(--line);border-radius:10px;cursor:pointer">
            Загрузить
            <input type="file" accept=".tar,.tgz,.gz,application/x-tar" style="display:none" :disabled="busyActive" @change="uploadBackup">
          </label>
        </div>
        <div class="domain-list" style="margin-top:0.8rem">
          <div v-for="b in (backups?.items || [])" :key="b.name" class="domain-item">
            <div>
              <code>{{ b.name }}</code>
              <div style="color:var(--muted);font-size:0.8rem">{{ b.date }} · {{ b.size ?? 0 }}</div>
            </div>
            <div class="row">
              <button type="button" class="ghost" :disabled="busyActive"
                :class="{ 'is-busy': busyButton === 'bak-dl' }"
                @click="downloadBackup(b.name)">Скачать</button>
              <button type="button" class="primary" :disabled="busyActive"
                :class="{ 'is-busy': busyButton === 'bak-restore' }"
                @click="restoreBackup(b.name)">Восстановить</button>
              <button type="button" class="ghost danger" :disabled="busyActive" @click="deleteBackup(b.name)">Удалить</button>
            </div>
          </div>
        </div>
      </section>

      <div id="grp-bypass" class="settings-group">
        <h2>{{ settingGroups[1].title }}</h2>
        <p>{{ settingGroups[1].desc }}</p>
      </div>

      <section id="sec-modes" class="panel">
        <div class="panel-h"><h2>Режимы обхода</h2></div>
        <p class="panel-d">Авторотация, hostlist, fallback, DNS, UDP games, RST, reasm, QUIC.</p>
        <div class="grid-2">
          <label v-for="m in modes" :key="m.key" class="toggle">
            <input type="checkbox" :checked="!!modeOn(m)" :disabled="busyActive"
              @change="toggleMode(m, ($event.target as HTMLInputElement).checked)">
            <span>{{ m.title }}</span>
            <span class="chip" :class="{ on: modeOn(m) }">{{ modeOn(m) ? 'вкл' : 'выкл' }}</span>
          </label>
        </div>
        <p v-if="udpGamesSettings?.ports" class="panel-d" style="margin-top:0.75rem">
          UDP Games порты: <code>{{ udpGamesSettings.ports }}</code>
          · стратегия профиля 7 — во вкладке «Стратегии».
        </p>
      </section>

      <section id="sec-tls" class="panel">
        <div class="panel-h"><h2>TLS blob</h2><span class="chip">{{ tlsBlobSettings?.current_mode || '—' }}</span></div>
        <p class="panel-d">Глобальный blob ClientHello для TLS-фейков.</p>
        <div class="grid-2">
          <label class="field">
            <span>Режим</span>
            <select v-model="tlsMode" :disabled="busyActive">
              <option value="fake_default_tls">fake_default_tls (встроенный)</option>
              <option value="custom">файл maxru</option>
            </select>
          </label>
          <label class="field">
            <span>Файл</span>
            <select v-model="tlsBlob" :disabled="busyActive || tlsMode === 'fake_default_tls'">
              <option v-for="b in (tlsBlobSettings?.available_blobs || [])" :key="b" :value="b">{{ b }}</option>
            </select>
          </label>
        </div>
        <div class="actions" style="margin-top:0.8rem">
          <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'tls' }" @click="saveTls">Сохранить TLS</button>
        </div>
      </section>

      <section id="sec-wg" class="panel">
        <div class="panel-h"><h2>WireGuard fakes</h2><span class="chip" :class="{ on: wgOn }">{{ wgOn ? 'вкл' : 'выкл' }}</span></div>
        <div class="grid-2">
          <label class="toggle"><input v-model="wgOn" type="checkbox" :disabled="busyActive"><span>Включить WG-стратегию</span></label>
          <label class="field">
            <span>Repeats</span>
            <input v-model.number="wgRepeats" type="number" min="2" max="99" :disabled="busyActive">
          </label>
          <label class="field" style="grid-column:1/-1">
            <span>Blob</span>
            <select v-model="wgBlob" :disabled="busyActive">
              <option v-for="b in (wgBlobSettings?.available_blobs || [])" :key="b" :value="b">{{ b }}</option>
            </select>
          </label>
        </div>
        <div class="actions" style="margin-top:0.8rem">
          <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'wg' }" @click="saveWg(true)">Сохранить WG</button>
        </div>
      </section>

      <section id="sec-wssize" class="panel">
        <div class="panel-h">
          <h2>WSSIZE ramp</h2>
          <span v-if="wssizeRampSettings" class="chip" :class="{ on: wssizeOn }">{{ wssizeOn ? 'вкл' : 'выкл' }}</span>
        </div>
        <p v-if="!wssizeRampSettings" class="panel-d">Настройка недоступна в этой сборке / ещё не загружена.</p>
        <template v-else>
          <p class="panel-d">
            Googlevideo IP: {{ wssizeRampSettings.googlevideo_ips }} ·
            локальный DNS-сбор: {{ wssizeRampSettings.googlevideo_local_collection }}
          </p>
          <div class="grid-2">
            <label class="toggle"><input v-model="wssizeOn" type="checkbox" :disabled="busyActive"><span>Включить wssize_ramp</span></label>
            <label class="field">
              <span>Режим</span>
              <select v-model="wssizeMode" :disabled="busyActive">
                <option value="full">full</option>
                <option value="lite">lite</option>
              </select>
            </label>
            <label class="field">
              <span>Scale (0–14)</span>
              <input v-model.number="wssizeScale" type="number" min="0" max="14" :disabled="busyActive">
            </label>
          </div>
          <div class="actions" style="margin-top:0.8rem">
            <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'wssize' }" @click="saveWssize">Сохранить</button>
          </div>
        </template>
      </section>

      <div id="grp-net" class="settings-group">
        <h2>{{ settingGroups[2].title }}</h2>
        <p>{{ settingGroups[2].desc }}</p>
      </div>

      <section id="sec-firewall" class="panel">
        <div class="panel-h"><h2>Firewall / Offload</h2></div>
        <p class="panel-d">Тип межсетевого экрана и flow offload.</p>
        <div class="grid-2">
          <label class="field">
            <span>FWTYPE</span>
            <select v-model="fw" :disabled="busyActive">
              <option value="iptables">iptables</option>
              <option value="nftables">nftables</option>
            </select>
          </label>
          <label class="field">
            <span>FLOWOFFLOAD</span>
            <select v-model="offload" :disabled="busyActive">
              <option value="donttouch">donttouch</option>
              <option value="none">none</option>
              <option value="software">software</option>
              <option value="hardware">hardware</option>
            </select>
          </label>
        </div>
        <div class="actions" style="margin-top:0.8rem">
          <button type="button" class="primary" :disabled="busyActive" @click="saveSelect('fwtype', fw, 'FWTYPE')">Сохранить FW</button>
          <button type="button" class="primary" :disabled="busyActive" @click="saveSelect('flowoffload', offload, 'Offload')">Сохранить Offload</button>
        </div>
      </section>

      <section id="sec-ifaces" class="panel">
        <div class="panel-h">
          <h2>Интерфейсы WAN / LAN</h2>
          <span v-if="ifacesSettings?.needs_autodetect" class="chip">нужно задать</span>
        </div>
        <p class="panel-d">
          На Keenetic/Entware без <code>IFACE_WAN</code>/<code>IFACE_LAN</code> правила NFQUEUE не цепляют трафик.
          WAN — uplink (default route), LAN — локальный bridge (обычно <code>br0</code>).
        </p>
        <p v-if="ifacesSettings?.suggested" class="panel-d">
          Автоопределение:
          WAN=<code>{{ ifacesSettings.suggested.wan || '—' }}</code>,
          LAN=<code>{{ ifacesSettings.suggested.lan || '—' }}</code>
          <template v-if="ifacesSettings.suggested.source"> · {{ ifacesSettings.suggested.source }}</template>
        </p>
        <div class="grid-2">
          <label class="field">
            <span>IFACE_WAN (внешний)</span>
            <select v-model="ifaceWan" :disabled="busyActive">
              <option v-for="n in ifaceOptions" :key="'w-'+n" :value="n">{{ n }}</option>
            </select>
          </label>
          <label class="field">
            <span>IFACE_LAN (локальный)</span>
            <select v-model="ifaceLan" :disabled="busyActive">
              <option v-for="n in ifaceOptions" :key="'l-'+n" :value="n">{{ n }}</option>
            </select>
          </label>
        </div>
        <div class="actions" style="margin-top:0.8rem">
          <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'ifaces' }" @click="saveIfaces">Сохранить</button>
          <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'ifaces-auto' }" @click="redetectIfaces">Определить заново</button>
        </div>
      </section>

      <section id="sec-ports" class="panel">
        <div class="panel-h"><h2>Порты NFQWS2</h2></div>
        <p class="panel-d">Дополнительные порты, которые обрабатывает nfqws2.</p>
        <div class="row">
          <select v-model="portProto" :disabled="busyActive" style="max-width:7rem">
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
          <input v-model="portValue" :disabled="busyActive" placeholder="443 или 1000-2000" style="flex:1;min-width:10rem">
          <button type="button" class="primary" :disabled="busyActive" @click="addPort">Добавить</button>
        </div>
        <p v-if="ports?.[portProto]?.full" class="panel-d">Текущие: <code>{{ ports[portProto]?.full }}</code></p>
        <div class="domain-list" style="margin-top:0.8rem">
          <div v-for="p in portList" :key="p" class="domain-item">
            <code>{{ p }}</code>
            <button type="button" class="ghost danger" :disabled="busyActive" @click="removePort(p)">Удалить</button>
          </div>
          <p v-if="!portList.length" class="panel-d">Пользовательских портов пока нет.</p>
        </div>
      </section>

      <section id="sec-scopes" class="panel">
        <div class="panel-h"><h2>Client scopes</h2><span class="chip" :class="{ on: scopeForm.enable }">{{ scopeForm.enable ? 'вкл' : 'выкл' }}</span></div>
        <p class="panel-d">Разные стратегии для разных клиентов по mark. Локи — во вкладке «Стратегии».</p>
        <div class="grid-2">
          <label class="toggle"><input v-model="scopeForm.enable" type="checkbox" :disabled="busyActive"><span>Включить режим mark</span></label>
          <label class="field"><span>mask</span><input v-model="scopeForm.mask" :disabled="busyActive"></label>
          <label class="field"><span>shift</span><input v-model="scopeForm.shift" type="number" min="0" max="31" :disabled="busyActive"></label>
          <label class="field"><span>max</span><input v-model="scopeForm.max" type="number" min="1" max="255" :disabled="busyActive"></label>
        </div>
        <div class="actions" style="margin-top:0.8rem">
          <button type="button" class="primary" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'scope' }" @click="saveScope">Сохранить scopes</button>
        </div>
        <hr>
        <p class="panel-d">IP клиента → mark scope. Устройства с этим IP будут использовать стратегии выбранного mark.</p>
        <div class="row">
          <input v-model="clientIP" :disabled="busyActive" placeholder="192.168.1.10 или IPv6" style="flex:1;min-width:12rem">
          <label class="field" style="max-width:9rem"><span>Mark ID</span><input v-model="clientScopeID" :disabled="busyActive" type="number" min="1" :max="scopes.max_scope || 255"></label>
          <button type="button" class="primary" :disabled="busyActive" @click="addClientScope">Добавить клиента</button>
        </div>
        <div class="domain-list" style="margin-top:0.8rem">
          <div v-for="mapping in clientMappings" :key="mapping.ip" class="domain-item">
            <code>{{ mapping.ip }}</code><span>{{ mapping.scope }}</span>
            <button type="button" class="ghost danger" :disabled="busyActive" @click="removeClientScope(mapping)">Удалить</button>
          </div>
          <p v-if="!clientMappings.length" class="panel-d">IP-маппинги ещё не добавлены.</p>
        </div>
      </section>

      <div id="grp-svc" class="settings-group">
        <h2>{{ settingGroups[3].title }}</h2>
        <p>{{ settingGroups[3].desc }}</p>
      </div>

      <section id="sec-config" class="panel">
        <div class="panel-h">
          <h2>Конфиг zapret2</h2>
          <button type="button" class="ghost" :disabled="busyActive" @click="loadConfig">Перечитать</button>
        </div>
        <p class="panel-d">Сырой файл · <code>{{ configPath || '/opt/zapret2/config' }}</code></p>
        <textarea v-if="configLoaded" v-model="configText" class="config-editor" :disabled="busyActive" spellcheck="false"></textarea>
        <p v-else class="loading">Загрузка конфига…</p>
        <div class="actions" style="margin-top:0.8rem">
          <button type="button" class="primary" :disabled="busyActive || !configLoaded" :class="{ 'is-busy': busyButton === 'cfg-save' }" @click="saveConfig">Сохранить и перезапустить</button>
        </div>
      </section>

      <section id="sec-errors" class="panel">
        <div class="panel-h">
          <h2>Ошибки nfqws2</h2>
          <button type="button" class="ghost" :disabled="busyActive" @click="loadErrors">Обновить</button>
        </div>
        <p class="panel-d">Последние сообщения демона nfqws2.</p>
        <p v-if="!errors.available" class="panel-d">Журнал пуст или не найден.</p>
        <template v-else>
          <p class="panel-d"><code>{{ errors.path }}</code> · строк: {{ errors.lines }}</p>
          <pre class="error-log">{{ errors.content || '(пусто)' }}</pre>
        </template>
      </section>
    </template>
  </section>
</template>
