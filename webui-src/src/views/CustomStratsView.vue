<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { customStrats } from '../api/endpoints'
import type { CustomBlockInfo, CustomStrategy } from '../api/types'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import { confirmDialog } from '../stores/confirm'
import { customBlocks, customStrategies, customStratsLoaded, refreshCustomStrats } from '../stores/customStrats'
import { refreshAll } from '../stores/status'
import { announceRestart, restartSuffix, showToast } from '../stores/toast'

const filter = ref('')
const hasDirty = ref(false)
const formOpen = ref(false)
const modalVisible = ref(false)
const editId = ref<number | null>(null)
const formBlock = ref('p5')
const formSpec = ref('')
const formComment = ref('')
const formError = ref('')
const specField = ref<HTMLTextAreaElement | null>(null)
const original = ref({ block: '', spec: '', comment: '' })

const importBlock = ref('tpl')
const importText = ref('')
const importErrors = ref<{ line: string; reason: string }[]>([])
const importNotes = ref<string[]>([])

const EXAMPLES: Record<string, { single: string; combo: string }> = {
  tcp: {
    single: 'fake:blob=maxru:tcp_md5:pos=2:repeats=2',
    combo: 'multisplit:blob=maxru:tcp_md5:pos=2:nodrop --lua-desync=fakeddisorder:pos=midsld:tcp_md5:nofake3',
  },
  udp: {
    single: 'fake:blob=quic_google:ipfrag:ipfrag_pos_udp=8:ipfrag_disorder:repeats=6:out_range=-d10',
    combo: 'send:repeats=2:ip_autottl=1,3-20 --lua-desync=fake:blob=0x00:repeats=4:out_range=-d10',
  },
}

const filtered = computed(() =>
  filter.value ? customStrategies.value.filter((s) => s.block === filter.value) : customStrategies.value)

const pendingCount = computed(() =>
  customStrategies.value.filter((s) => s.enabled && !s.assigned).length)

function blockInfo(block: string): CustomBlockInfo | undefined {
  return customBlocks.value.find((b) => b.block === block)
}

const formExamples = computed(() => {
  const tr = blockInfo(formBlock.value)?.transport
  return EXAMPLES[tr === 'udp' ? 'udp' : 'tcp']
})

const saveDisabled = computed(() => {
  const spec = formSpec.value.trim()
  if (busyActive.value) return true
  if (editId.value === null) return !spec
  return formBlock.value === original.value.block
    && spec === original.value.spec
    && formComment.value.trim() === original.value.comment
})

async function load() {
  await refreshCustomStrats('')
}

async function refresh() {
  try {
    await withBusy('refresh-custom-strats', load)
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

onMounted(refresh)

function highlightRow(id: number) {
  nextTick(() => {
    const el = document.getElementById(`custom-strat-row-${id}`)
    if (!el) return
    el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    el.classList.add('is-target')
    window.setTimeout(() => el.classList.remove('is-target'), 5000)
  })
}

function openAdd() {
  editId.value = null
  formBlock.value = filter.value || customBlocks.value[0]?.block || 'p5'
  formSpec.value = ''
  formComment.value = ''
  formError.value = ''
  original.value = { block: formBlock.value, spec: '', comment: '' }
  formOpen.value = true
}

function openEdit(item: CustomStrategy) {
  editId.value = item.id
  formBlock.value = item.block
  formSpec.value = item.spec
  formComment.value = item.comment
  formError.value = ''
  original.value = { block: item.block, spec: item.spec, comment: item.comment }
  formOpen.value = true
}

function cancelForm() {
  formOpen.value = false
  formError.value = ''
}

watch(formOpen, (open) => {
  if (!open) return
  modalVisible.value = false
  requestAnimationFrame(() => {
    modalVisible.value = true
    nextTick(() => specField.value?.focus())
  })
})

function onKeydown(event: KeyboardEvent) {
  if (!formOpen.value) return
  if (event.key === 'Escape') {
    event.preventDefault()
    cancelForm()
  }
}
onMounted(() => document.addEventListener('keydown', onKeydown, true))
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown, true))

async function saveForm() {
  const spec = formSpec.value.trim()
  if (!spec) {
    formError.value = 'Пустая стратегия.'
    return
  }
  formError.value = ''
  const wasEdit = editId.value !== null
  const targetId = editId.value
  try {
    await withBusy(wasEdit ? `custom-edit-${targetId}` : 'custom-add', async () => {
      let newId: number | null = null
      if (wasEdit) {
        await customStrats.edit(String(targetId), formBlock.value, spec, formComment.value.trim())
      } else {
        const res = await customStrats.add(formBlock.value, spec, formComment.value.trim())
        newId = typeof res?.id === 'number' ? res.id : null
      }
      formOpen.value = false
      hasDirty.value = true
      if (!wasEdit && filter.value && filter.value !== formBlock.value) filter.value = ''
      showToast(wasEdit
        ? `Запись #${targetId} изменена. Нажмите «Применить к конфигу».`
        : 'Стратегия добавлена. Нажмите «Применить к конфигу».')
      await load()
      if (wasEdit) highlightRow(targetId as number)
      else if (newId !== null) highlightRow(newId)
    })
  } catch (e) {
    formError.value = (e as Error).message
  }
}

async function toggleEnabled(item: CustomStrategy) {
  try {
    await withBusy(`custom-enable-${item.id}`, async () => {
      await customStrats.enable(String(item.id), !item.enabled)
      hasDirty.value = true
      showToast(item.enabled
        ? `Запись #${item.id} выключена.`
        : `Запись #${item.id} включена. Нажмите «Применить к конфигу».`)
      await load()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function removeItem(item: CustomStrategy) {
  const ok = await confirmDialog({
    title: `Удалить стратегию #${item.id}?`,
    message: 'При следующем применении номера оставшихся записей будут переназначены, а локи перенесены.',
    confirmText: 'Удалить',
    danger: true,
  })
  if (!ok) return
  try {
    await withBusy(`custom-delete-${item.id}`, async () => {
      await customStrats.remove(String(item.id))
      hasDirty.value = true
      showToast('Удалено. Нажмите «Применить к конфигу».')
      await load()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function importLines() {
  if (!importText.value.trim()) {
    showToast('Вставьте строки для импорта.', 'warning')
    return
  }
  try {
    await withBusy('custom-import', async () => {
      const res = await customStrats.import(importBlock.value, importText.value)
      importErrors.value = Array.isArray(res?.errors) ? res.errors : []
      importNotes.value = Array.isArray(res?.notes) ? res.notes : []
      if ((res?.added ?? 0) > 0) hasDirty.value = true
      showToast(
        `Импорт: добавлено ${res?.added ?? 0}, дубликатов ${res?.dups ?? 0}, пропущено ${res?.skipped ?? 0}.`
        + (importNotes.value.length ? ` Замен блобов: ${importNotes.value.length}.` : ''),
      )
      const total = res?.total ?? 0
      const limit = res?.limit ?? 256
      if (total > limit) {
        showToast(`Обработаны первые ${limit} из ${total} строк; остальные импортируйте отдельно.`, 'warning')
      }
      importText.value = ''
      await load()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function applyToConfig() {
  const ok = await confirmDialog({
    title: 'Применить свои стратегии к конфигу?',
    message: 'Стратегии будут встроены в config zapret2 с автоматической нумерацией, активные локи перенесены, служба перезапустится.',
    confirmText: 'Применить',
  })
  if (!ok) return
  try {
    await withBusy('custom-apply', async () => {
      announceRestart()
      const res = await customStrats.apply()
      hasDirty.value = false
      const remapBits: string[] = []
      if (res?.moved) remapBits.push(`перенесено локов: ${res.moved}`)
      if (res?.dropped) remapBits.push(`сброшено локов: ${res.dropped}`)
      const extra = remapBits.length ? ` (${remapBits.join(', ')})` : ''
      showToast(`Применено${extra}.${restartSuffix(res)}`)
      await Promise.all([load(), refreshAll()])
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}
</script>

<template>
  <section class="page-stack">
    <div class="page-head">
      <div>
        <span class="eyebrow">Конструктор zapret2</span>
        <h1>Свои стратегии</h1>
        <p>Добавление, редактирование, пакетный импорт из blockcheck2 и безопасный инжект в конфиг с сохранением локов.</p>
      </div>
      <div class="actions">
        <label class="field scope-picker">
          <span>Блок</span>
          <select id="custom-strats-filter" v-model="filter" :disabled="busyActive">
            <option value="">Все блоки</option>
            <option v-for="b in customBlocks" :key="b.block" :value="b.block">{{ b.block }} — {{ b.label }}</option>
          </select>
        </label>
        <button id="custom-strat-add-btn" type="button" :disabled="busyActive" @click="openAdd">Добавить стратегию</button>
        <button id="custom-apply-btn" type="button" class="primary"
          :class="{ 'is-busy': busyButton === 'custom-apply' }"
          :disabled="busyActive || (!customStrategies.length && !hasDirty)"
          @click="applyToConfig">
          Применить к конфигу{{ pendingCount ? ` (${pendingCount})` : hasDirty ? ' *' : '' }}
        </button>
        <button id="refresh-custom-strats" type="button"
          :class="{ 'is-busy': busyButton === 'refresh-custom-strats' }"
          :disabled="busyActive" @click="refresh">Обновить</button>
      </div>
    </div>

    <div v-if="!customStratsLoaded" class="loading">Загрузка…</div>
    <template v-else>
      <section id="custom-strats-panel" class="panel">
        <div class="panel-h">
          <h2>Список пользовательских стратегий</h2>
          <span class="chip" :class="{ on: customStrategies.length > 0 }">{{ filtered.length }}</span>
        </div>
        <p v-if="!filtered.length" class="panel-d">
          В выбранном фильтре стратегий пока нет. Нажмите «Добавить стратегию» или используйте импорт ниже.
        </p>
        <div v-else class="strat-list">
          <article v-for="item in filtered" :id="`custom-strat-row-${item.id}`" :key="item.id"
            class="strat-row" :class="{ 'is-off': !item.enabled }">
            <div class="strat-head">
              <span class="chip">ID {{ item.id }}</span>
              <span class="chip">{{ item.block }}</span>
              <span v-if="!item.enabled" class="chip">выкл</span>
              <span v-else-if="item.assigned" class="chip on">#{{ item.assigned }}</span>
              <span v-else class="chip is-pending">не применена</span>
              <span v-if="item.comment" class="strat-comment">{{ item.comment }}</span>
            </div>
            <pre class="mono strat-spec">{{ item.spec }}</pre>
            <div class="actions">
              <button type="button" :disabled="busyActive"
                :class="{ 'is-busy': busyButton === `custom-enable-${item.id}` }"
                @click="toggleEnabled(item)">{{ item.enabled ? 'Выключить' : 'Включить' }}</button>
              <button type="button" class="ghost" :disabled="busyActive" @click="openEdit(item)">Изменить</button>
              <button type="button" class="ghost danger" :disabled="busyActive"
                :class="{ 'is-busy': busyButton === `custom-delete-${item.id}` }"
                @click="removeItem(item)">Удалить</button>
            </div>
          </article>
        </div>
      </section>

      <section id="custom-import-panel" class="panel">
        <div class="panel-h">
          <h2>Пакетный импорт (blockcheck2 / nfqws2)</h2>
          <span class="chip">до 256 записей</span>
        </div>
        <p class="panel-d">
          Вставьте строки из SUMMARY blockcheck2, команды nfqws2 или готовые <code>--lua-desync=...</code>. Неизвестные блобы заменятся на штатные, стратегии от zapret v1 будут отклонены.
        </p>
        <div class="grid-2" style="margin-bottom:0.65rem">
          <label class="field">
            <span>Целевой блок</span>
            <select id="custom-import-block" v-model="importBlock" :disabled="busyActive">
              <option v-for="b in customBlocks" :key="b.block" :value="b.block">
                {{ b.block }} — {{ b.label }} (профили {{ b.profiles }})
              </option>
            </select>
          </label>
        </div>
        <label class="field">
          <span>Строки стратегий</span>
          <textarea id="custom-import-text" v-model="importText" rows="5" :disabled="busyActive"
            placeholder="nfqws2 --lua-desync=fake:blob=fake_default_tls:repeats=2 --lua-desync=multisplit:pos=1,midsld"></textarea>
        </label>
        <div class="actions" style="margin-top:0.75rem">
          <button id="custom-import-btn" type="button" class="primary"
            :class="{ 'is-busy': busyButton === 'custom-import' }"
            :disabled="busyActive || !importText.trim()" @click="importLines">Импортировать</button>
        </div>
        <div v-if="importNotes.length" class="import-feedback">
          <strong>Замены блобов:</strong>
          <ul>
            <li v-for="(n, i) in importNotes" :key="i">{{ n }}</li>
          </ul>
        </div>
        <div v-if="importErrors.length" class="import-feedback is-error">
          <strong>Отклонённые строки ({{ importErrors.length }}):</strong>
          <ul>
            <li v-for="(err, i) in importErrors" :key="i">
              <code>{{ err.line }}</code> — {{ err.reason }}
            </li>
          </ul>
        </div>
      </section>

      <div v-if="formOpen" :class="['modal-overlay', { 'is-visible': modalVisible }]"
        role="dialog" aria-modal="true" @click.self="cancelForm">
        <form id="custom-strat-form" class="modal-card modal-wide" @submit.prevent="saveForm">
          <h3 class="modal-title">{{ editId === null ? 'Новая стратегия' : `Стратегия #${editId}` }}</h3>
          <div class="modal-fields">
            <label class="field">
              <span>Блок</span>
              <select id="custom-strat-block" v-model="formBlock" :disabled="busyActive">
                <option v-for="b in customBlocks" :key="b.block" :value="b.block">
                  {{ b.block }} — {{ b.label }} ({{ b.transport.toUpperCase() }})
                </option>
              </select>
            </label>
            <label class="field">
              <span>Спецификация desync</span>
              <textarea id="custom-strat-spec" ref="specField" v-model="formSpec" rows="4" :disabled="busyActive"
                placeholder="fake:blob=maxru:tcp_md5:pos=2:repeats=2"></textarea>
            </label>
            <div class="example-box">
              <span>Пример: <code>{{ formExamples.single }}</code></span>
              <button type="button" class="ghost example-btn" :disabled="busyActive"
                @click="formSpec = formExamples.single; formError = ''">Вставить</button>
              <div>Комбо: <code>{{ formExamples.combo }}</code></div>
            </div>
            <label class="field">
              <span>Комментарий (необязательно)</span>
              <input id="custom-strat-comment" v-model="formComment" type="text" maxlength="64" :disabled="busyActive">
            </label>
            <p v-if="formError" id="custom-strat-error" class="err-hint" role="alert">{{ formError }}</p>
          </div>
          <div class="modal-actions">
            <button id="custom-strat-cancel" type="button" class="ghost" :disabled="busyActive" @click="cancelForm">Отмена</button>
            <button id="custom-strat-save" type="submit" class="primary"
              :class="{ 'is-busy': busyButton === (editId === null ? 'custom-add' : `custom-edit-${editId}`) }"
              :disabled="saveDisabled">{{ editId === null ? 'Добавить' : 'Сохранить' }}</button>
          </div>
        </form>
      </div>
    </template>
  </section>
</template>

<style scoped>
.strat-list { display: grid; gap: 0.6rem; }
.strat-row {
  display: grid;
  gap: 0.45rem;
  padding: 0.7rem 0.75rem;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--bg1) 50%, transparent);
}
.strat-row.is-off { opacity: 0.65; }
.strat-row.is-target { border-color: var(--accent); }
.strat-head { display: flex; flex-wrap: wrap; gap: 0.35rem; align-items: center; }
.strat-comment { color: var(--muted); font-size: 0.84rem; }
.chip.is-pending { color: var(--warn); border-color: color-mix(in srgb, var(--warn) 45%, var(--line)); font-weight: 600; }
.strat-spec {
  margin: 0;
  padding: 0.5rem 0.6rem;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 0.8rem;
}
.import-feedback {
  margin-top: 0.75rem;
  padding: 0.65rem 0.75rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--bg1) 55%, transparent);
  font-size: 0.82rem;
}
.import-feedback.is-error { border-color: color-mix(in srgb, var(--danger) 45%, var(--line)); }
.import-feedback ul { margin: 0.35rem 0 0; padding-left: 1.2rem; }
.modal-wide { width: min(580px, 100%); }
.modal-fields { display: grid; gap: 0.65rem; margin: 0.65rem 0; }
.example-box { font-size: 0.78rem; color: var(--muted); display: grid; gap: 0.3rem; }
.example-box code { color: var(--ink); overflow-wrap: anywhere; }
.example-btn { width: fit-content; padding: 0.2rem 0.55rem; font-size: 0.76rem; }
.err-hint { color: var(--danger); font-size: 0.84rem; margin: 0; }
</style>
