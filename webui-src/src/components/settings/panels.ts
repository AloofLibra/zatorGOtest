import type { Component } from 'vue'
import ModeTogglePanel from './ModeTogglePanel.vue'
import TlsBlobPanel from './TlsBlobPanel.vue'
import FallbackPanel from './FallbackPanel.vue'
import UdpGamesPanel from './UdpGamesPanel.vue'
import WireGuardPanel from './WireGuardPanel.vue'
import PortsPanel from './PortsPanel.vue'
import ProviderPanel from './ProviderPanel.vue'
import BackupsPanel from './BackupsPanel.vue'
import WssizeRampPanel from './WssizeRampPanel.vue'
import SelectSettingPanel from './SelectSettingPanel.vue'
import ClientScopePanel from './ClientScopePanel.vue'
import ConfigEditorPanel from './ConfigEditorPanel.vue'
import UpdatesPanel from './UpdatesPanel.vue'
import ErrorsPanel from './ErrorsPanel.vue'

export interface SettingsPanelEntry {
  id: string
  component: Component
  props?: Record<string, unknown>
}

export const settingsPanels: SettingsPanelEntry[] = [
  { id: 'updates', component: UpdatesPanel },
  { id: 'tls-blob', component: TlsBlobPanel },
  { id: 'wssize-ramp', component: WssizeRampPanel },
  { id: 'auto-mode', component: ModeTogglePanel, props: { setting: 'auto_mode' } },
  { id: 'fallback', component: FallbackPanel },
  { id: 'hostlist', component: ModeTogglePanel, props: { setting: 'hostlist' } },
  { id: 'udp-games', component: UdpGamesPanel },
  { id: 'dns-desync', component: ModeTogglePanel, props: { setting: 'dns_desync' } },
  {
    id: 'fwtype',
    component: SelectSettingPanel,
    props: {
      setting: 'fwtype',
      title: 'Firewall backend (iptables / nftables)',
      descriptions: [
        'Переключатель zapret2 между iptables и nftables. Актуально для OpenWRT 21+ и может помочь с войсами Discord.',
        'При сохранении zapret2 перезапускается автоматически.',
      ],
      options: [
        { value: 'iptables', label: 'iptables' },
        { value: 'nftables', label: 'nftables' },
      ],
    },
  },
  {
    id: 'flowoffload',
    component: SelectSettingPanel,
    props: {
      setting: 'flowoffload',
      title: 'Аппаратное ускорение (flow offload)',
      descriptions: [
        'Управление hardware/software offload в zapret2. Может увеличить скорость на роутере, но иногда ломает DPI-обход.',
        'donttouch — не трогать системные настройки; none — выключить; software/hardware — принудительный режим.',
      ],
      options: [
        { value: 'donttouch', label: 'donttouch (не трогать)' },
        { value: 'none', label: 'none (выключить)' },
        { value: 'software', label: 'software' },
        { value: 'hardware', label: 'hardware' },
      ],
    },
  },
  { id: 'wireguard', component: WireGuardPanel },
  { id: 'rst-guard', component: ModeTogglePanel, props: { setting: 'rst_guard' } },
  { id: 'reasm', component: ModeTogglePanel, props: { setting: 'reasm' } },
  { id: 'quic443', component: ModeTogglePanel, props: { setting: 'quic443' } },
  { id: 'ports', component: PortsPanel },
  { id: 'client-scope', component: ClientScopePanel },
  { id: 'provider', component: ProviderPanel },
  { id: 'backups', component: BackupsPanel },
  { id: 'config', component: ConfigEditorPanel },
  { id: 'errors', component: ErrorsPanel },
]
