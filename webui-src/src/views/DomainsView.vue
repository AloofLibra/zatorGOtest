<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { domains } from '../api/endpoints'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import {
  DOMAIN_LISTS, DOMAIN_META, domainChecks, domainLists, domainsLoaded, refreshDomainList, refreshDomains, type DomainListName,
} from '../stores/domains'
import { modeSettings } from '../stores/settings'
import { scope, scopes } from '../stores/status'
import { showToast } from '../stores/toast'
import { confirmDialog } from '../stores/confirm'

const route = useRoute()
const router = useRouter()
const input = ref('')
const bulk = ref('')
const strategyDraft = ref<Record<string, string>>({})

const labels: Record<DomainListName, string> = {
  netrogat: 'Исключения',
  custom_rkn: 'TCP Custom',
  substring: 'Подстроки RKN',
  netrogat_substring: 'Подстроки исключений',
}

const active = computed<DomainListName>(() => {
  const p = String(route.params.list || 'netrogat')
  return (DOMAIN_LISTS as readonly string[]).includes(p) ? p as DomainListName : 'netrogat'
})
const list = computed(() => domainLists[active.value])
const meta = computed(() => DOMAIN_META[active.value])

onMounted(() => {
  if (!domainsLoaded.value) refreshDomains()
})
const hostlistAuto = computed(() => !!modeSettings.hostlist?.auto)
const autoLocked = computed(() => hostlistAuto.value && (active.value === 'custom_rkn' || active.value === 'substring'))
const scopeOptions = computed(() => scopes.value.scopes || ['default'])

watch(active, (name) => {
  if (!domainLists[name]) refreshDomainList(name)
})
watch(scope, () => {
  void refreshDomainList('custom_rkn')
})

function changeScope(v: string) {
  scope.value = v || 'default'
  router.replace({ query: { ...route.query, scope: scope.value } })
}

watch(list, (payload) => {
  if (!payload?.items) return
  const next: Record<string, string> = { ...strategyDraft.value }
  const alive = new Set(payload.items.map((i) => i.value))
  for (const key of Object.keys(next)) {
    if (!alive.has(key)) delete next[key]
  }
  for (const item of payload.items) {
    if (next[item.value] === undefined) {
      next[item.value] = item.strategy != null ? String(item.strategy) : ''
    } else if (!String(next[item.value] ?? '').trim() && item.strategy != null) {
      next[item.value] = String(item.strategy)
    }
  }
  strategyDraft.value = next
}, { deep: true, immediate: true })

function go(name: DomainListName) {
  router.push(`/domains/${name}`)
}

async function add() {
  const d = input.value.trim()
  if (!d) return
  try {
    await withBusy('domain-add', async () => {
      await domains.add(active.value, d, scope.value)
      input.value = ''
      await refreshDomainList(active.value)
    })
    showToast('Добавлено')
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function remove(domain: string) {
  if (autoLocked.value) return
  const ok = await confirmDialog({
    title: 'Удалить запись?',
    message: `Удалить «${domain}» из списка?`,
    danger: true,
  })
  if (!ok) return
  try {
    await withBusy('domain-rm', async () => {
      const res = await domains.remove(active.value, domain, scope.value)
      delete strategyDraft.value[domain]
      await refreshDomainList(active.value)
      const warn = String((res as { warning?: string }).warning || '')
      if (warn) showToast(warn, 'warning')
      else showToast('Удалено')
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function importList() {
  if (!bulk.value.trim()) return
  try {
    await withBusy('domain-import', async () => {
      const res = await domains.import(active.value, bulk.value, scope.value)
      bulk.value = ''
      await refreshDomainList(active.value)
      showToast(`Импорт: +${res.added || 0}`)
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function refreshLists() {
  try {
    await withBusy('domains-refresh', refreshDomains)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function setStrategy(domain: string) {
  const raw = String(strategyDraft.value[domain] ?? '').trim()
  if (raw && (!/^\d+$/.test(raw) || Number(raw) < 0)) {
    showToast('Некорректный номер стратегии', 'error')
    return
  }
  try {
    await withBusy(`domain-st-${domain}`, async () => {
      if (!raw || raw === '0') {
        await domains.clear_strategy(domain, scope.value)
        strategyDraft.value[domain] = ''
      } else {
        await domains.set_strategy(domain, raw, scope.value)
        strategyDraft.value[domain] = raw
      }
      await refreshDomainList(active.value)
      showToast((!raw || raw === '0') ? `Стратегия снята с ${domain}` : `Стратегия ${raw} для ${domain}`)
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function clearAll() {
  if (autoLocked.value) return
  const ok = await confirmDialog({
    title: 'Очистить список?',
    message: 'Все записи будут удалены.',
    danger: true,
  })
  if (!ok) return
  try {
    await withBusy('domain-clear', async () => {
      const res = await domains.clear(active.value, scope.value)
      strategyDraft.value = {}
      await refreshDomainList(active.value)
      const warn = String((res as { warning?: string }).warning || '')
      showToast(warn || 'Список очищен', warn ? 'warning' : undefined)
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function checkDomain(domain: string) {
  try {
    await withBusy(`domain-check-${domain}`, async () => {
      const res = await domains.check(domain)
      if (res.check) domainChecks[domain] = res.check
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}
</script>

<template>
  <section>
    <div class="page-head">
      <div>
        <h1>Домены</h1>
        <p>Списки hostlist: исключения, custom RKN и подстроки. Переключайте вкладки ниже.</p>
      </div>
      <div class="actions">
        <label v-if="scopes.enabled" class="field" style="margin:0">
          <span>Scope</span>
          <select :value="scope" :disabled="busyActive" @change="changeScope(($event.target as HTMLSelectElement).value)">
            <option v-for="s in scopeOptions" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>
        <button type="button" :disabled="busyActive" :class="{ 'is-busy': busyButton === 'domains-refresh' }"
          @click="refreshLists">Обновить</button>
      </div>
    </div>

    <div class="subtabs">
      <button v-for="name in DOMAIN_LISTS" :key="name" type="button"
        :class="{ 'is-active': active === name }" :disabled="busyActive" @click="go(name)">
        {{ labels[name] }}
      </button>
    </div>

    <div class="panel">
      <div class="panel-h">
        <h2>{{ list?.title || labels[active] }}</h2>
        <span class="chip">{{ list?.items?.length ?? 0 }}</span>
      </div>
      <p class="panel-d">{{ list?.description || meta.addLabel }}</p>
      <p v-if="autoLocked" class="panel-d" style="color:var(--warn)">
        Autohostlist включён: ручное изменение этого списка заблокировано. Выключите автосбор в Настройках → Режимы.
      </p>

      <form class="row" @submit.prevent="add">
        <input v-model="input" :placeholder="meta.placeholder" :disabled="busyActive || autoLocked" style="flex:1;min-width:12rem">
        <button type="submit" class="primary" :disabled="busyActive || autoLocked" :class="{ 'is-busy': busyButton === 'domain-add' }">Добавить</button>
        <button type="button" class="ghost danger"
          :disabled="busyActive || autoLocked"
          @click="clearAll">Очистить</button>
      </form>

      <div class="field" style="margin-top:0.9rem">
        <span>Импорт списком</span>
        <textarea v-model="bulk"
          :disabled="busyActive || autoLocked"
          rows="4" placeholder="по одному на строку"></textarea>
        <div class="actions" style="margin-top:0.5rem">
          <button type="button"
            :disabled="busyActive || autoLocked"
            :class="{ 'is-busy': busyButton === 'domain-import' }"
            @click="importList">Импортировать</button>
        </div>
      </div>
    </div>

    <div class="domain-list">
      <div v-for="item in (list?.items || [])" :key="item.value" class="domain-item">
        <div>
          <code>{{ item.value }}</code>
          <div v-if="active === 'custom_rkn'" class="row" style="margin-top:0.45rem">
            <input
              :value="strategyDraft[item.value] ?? (item.strategy != null ? String(item.strategy) : '')"
              type="number" min="1" placeholder="стратегия"
              :disabled="busyActive" style="max-width:7rem"
              @input="strategyDraft[item.value] = ($event.target as HTMLInputElement).value"
            >
            <button type="button" :disabled="busyActive"
              :class="{ 'is-busy': busyButton === `domain-st-${item.value}` }"
              @click="setStrategy(item.value)">Стратегия</button>
          </div>
        </div>
        <div class="row">
          <button v-if="active === 'custom_rkn'" type="button" class="ghost" :disabled="busyActive"
            :class="{ 'is-busy': busyButton === `domain-check-${item.value}` }"
            @click="checkDomain(item.value)">Проверить</button>
          <button type="button" class="ghost danger" :disabled="busyActive || autoLocked"
            @click="remove(item.value)">Удалить</button>
        </div>
        <p v-if="domainChecks[item.value]" class="panel-d" style="grid-column:1/-1;margin:0.35rem 0 0">
          {{ (domainChecks[item.value].results || []).map(r => r.verdict || r.text).join(' · ') || domainChecks[item.value].message || 'Готово' }}
        </p>
      </div>
      <p v-if="domainsLoaded && !(list?.items || []).length" class="panel-d">Список пуст.</p>
    </div>
  </section>
</template>
